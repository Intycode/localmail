package imap

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ErrIdleUnsupported reports that the server does not advertise IDLE: the
// caller should fall back to periodic polling.
var ErrIdleUnsupported = errors.New("il server non supporta IDLE")

// Dial connects, upgrades TLS (when configured) and authenticates.
// The returned Session keeps the password only inside the client.
func Dial(cfg Config, password string) (Session, error) {
	if err := ValidateConfig(cfg, true); err != nil {
		return nil, err
	}
	// the session object exists before dialing so the unilateral-data hook
	// can route server updates to whoever is idling
	cs := &clientSession{cfg: cfg}
	opts := &imapclient.Options{
		TLSConfig: TLSConfig(cfg),
		Dialer:    dialer(),
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages == nil {
					return
				}
				cs.mu.Lock()
				ch := cs.notify
				cs.mu.Unlock()
				if ch == nil {
					return
				}
				select {
				case ch <- struct{}{}:
				default: // never block the client read loop
				}
			},
		},
	}

	var c *imapclient.Client
	var err error
	switch cfg.Security {
	case SecurityTLS:
		c, err = imapclient.DialTLS(Address(cfg), opts)
		if err != nil {
			return nil, fmt.Errorf("connessione TLS a %s non riuscita: %v", cfg.Host, err)
		}
	case SecurityStartTLS:
		c, err = imapclient.DialStartTLS(Address(cfg), opts)
		if err != nil {
			return nil, fmt.Errorf("STARTTLS con %s non riuscito: %v", cfg.Host, err)
		}
	case SecurityPlain:
		c, err = imapclient.DialInsecure(Address(cfg), opts)
		if err != nil {
			return nil, fmt.Errorf("connessione a %s non riuscita: %v", cfg.Host, err)
		}
	default:
		return nil, fmt.Errorf("modalità di sicurezza sconosciuta: %q", cfg.Security)
	}

	if cfg.Auth == "oauth" {
		// OAuth2 (Gmail): SASL XOAUTH2 with the access token as credential.
		if err := c.Authenticate(xoauth2Client{user: cfg.Username, token: password}); err != nil {
			c.Close()
			return nil, fmt.Errorf("autenticazione OAuth non riuscita per %s (%v)", cfg.Username, err)
		}
	} else if err := c.Login(cfg.Username, password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("autenticazione non riuscita per %s: controlla username e password/app-password (%v)", cfg.Username, err)
	}
	cs.c = c
	return cs, nil
}

// xoauth2Client implements sasl.Client for the XOAUTH2 mechanism (Gmail):
// the initial response is "user=<u>\x01auth=Bearer <t>\x01\01"; the server
// never challenges on success (an empty challenge means failure).
type xoauth2Client struct {
	user  string
	token string
}

func (c xoauth2Client) Start() (string, []byte, error) {
	resp := "user=" + c.user + "\x01" + "auth=Bearer " + c.token + "\x01\x01"
	return "XOAUTH2", []byte(resp), nil
}

func (c xoauth2Client) Next(challenge []byte) ([]byte, error) {
	return nil, fmt.Errorf("sfida XOAUTH2 inattesa dal server")
}

// clientSession implements Session over an imapclient.Client.
type clientSession struct {
	cfg     Config
	c       *imapclient.Client
	current string // currently selected mailbox

	mu     sync.Mutex
	notify chan<- struct{} // push target while idling (nil otherwise)
}

var _ Session = (*clientSession)(nil)

func (s *clientSession) Close() error { return s.c.Close() }

func (s *clientSession) Folders() ([]FolderInfo, error) {
	cmd := s.c.List("", "*", &imap.ListOptions{ReturnSpecialUse: true})
	list, err := cmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("list cartelle: %v", err)
	}
	out := make([]FolderInfo, 0, len(list))
	for _, l := range list {
		info := FolderInfo{Name: l.Mailbox}
		for _, a := range l.Attrs {
			switch a {
			case imap.MailboxAttrNoSelect:
				info.NoSelect = true
			case imap.MailboxAttrSent:
				info.SpecialUse = string(a)
			case imap.MailboxAttrDrafts:
				info.SpecialUse = string(a)
			case imap.MailboxAttrTrash:
				info.SpecialUse = string(a)
			case imap.MailboxAttrArchive:
				info.SpecialUse = string(a)
			case imap.MailboxAttrJunk:
				info.SpecialUse = string(a)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *clientSession) Select(mailbox string) (SelectState, error) {
	data, err := s.c.Select(mailbox, nil).Wait()
	if err != nil {
		return SelectState{}, fmt.Errorf("select %q: %v", mailbox, err)
	}
	s.current = mailbox
	return SelectState{UIDValidity: data.UIDValidity, Exists: data.NumMessages}, nil
}

func (s *clientSession) UIDs() ([]uint32, error) {
	data, err := s.c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("uid search: %v", err)
	}
	uids := data.AllUIDs()
	out := make([]uint32, 0, len(uids))
	for _, u := range uids {
		out = append(out, uint32(u))
	}
	return out, nil
}

// Fetch retrieves raw MIME in batches with BODY.PEEK so reading a message on
// the server never marks it as seen.
func (s *clientSession) Fetch(uids []uint32) ([]RawMessage, error) {
	out := make([]RawMessage, 0, len(uids))
	section := &imap.FetchItemBodySection{Peek: true}
	for start := 0; start < len(uids); start += FetchBatch {
		end := start + FetchBatch
		if end > len(uids) {
			end = len(uids)
		}
		batch := uids[start:end]
		set := imap.UIDSetNum()
		for _, u := range batch {
			set.AddNum(imap.UID(u))
		}
		opts := &imap.FetchOptions{
			UID:         true,
			Flags:       true,
			Envelope:    true,
			InternalDate: true,
			RFC822Size:  true,
			BodySection: []*imap.FetchItemBodySection{section},
		}
		msgs, err := s.c.Fetch(set, opts).Collect()
		if err != nil {
			return out, fmt.Errorf("fetch messaggi: %v", err)
		}
		for _, m := range msgs {
			body := m.FindBodySection(section)
			if len(body) == 0 {
				continue
			}
			flags := make([]string, 0, len(m.Flags))
			for _, f := range m.Flags {
				flags = append(flags, string(f))
			}
			out = append(out, RawMessage{UID: uint32(m.UID), Flags: flags, Data: body})
		}
	}
	return out, nil
}

func (s *clientSession) Flags(uids []uint32) (map[uint32][]string, error) {
	out := make(map[uint32][]string, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	msgs, err := s.c.Fetch(s.uidSet(uids), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch flags: %v", err)
	}
	for _, m := range msgs {
		flags := make([]string, 0, len(m.Flags))
		for _, f := range m.Flags {
			flags = append(flags, string(f))
		}
		out[uint32(m.UID)] = flags
	}
	return out, nil
}

func (s *clientSession) ensureSelected(mailbox string) error {
	if s.current == mailbox {
		return nil
	}
	_, err := s.Select(mailbox)
	return err
}

func (s *clientSession) uidSet(uids []uint32) imap.UIDSet {
	set := imap.UIDSetNum()
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	return set
}

func (s *clientSession) MarkSeen(uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	if err := s.ensureSelected(s.current); err != nil {
		return err
	}
	store := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}, Silent: true}
	if err := s.c.Store(s.uidSet(uids), store, nil).Close(); err != nil {
		return fmt.Errorf("store seen: %v", err)
	}
	return nil
}

// MarkUnseen clears \Seen on the given UIDs (STORE -FLAGS (\Seen)): the
// server-side counterpart of marking a message unread.
func (s *clientSession) MarkUnseen(uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	if err := s.ensureSelected(s.current); err != nil {
		return err
	}
	store := &imap.StoreFlags{Op: imap.StoreFlagsDel, Flags: []imap.Flag{imap.FlagSeen}, Silent: true}
	if err := s.c.Store(s.uidSet(uids), store, nil).Close(); err != nil {
		return fmt.Errorf("store unseen: %v", err)
	}
	return nil
}

func (s *clientSession) MoveTo(uids []uint32, mailbox string) error {
	if len(uids) == 0 {
		return nil
	}
	if err := s.ensureSelected(s.current); err != nil {
		return err
	}
	if _, err := s.c.Move(s.uidSet(uids), mailbox).Wait(); err != nil {
		return fmt.Errorf("move a %q: %v", mailbox, err)
	}
	return nil
}

func (s *clientSession) Delete(uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	if err := s.ensureSelected(s.current); err != nil {
		return err
	}
	store := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}
	if err := s.c.Store(s.uidSet(uids), store, nil).Close(); err != nil {
		return fmt.Errorf("store deleted: %v", err)
	}
	// UID EXPUNGE removes only these UIDs; a plain EXPUNGE would also erase
	// messages another client merely marked \Deleted
	if s.c.Caps().Has(imap.CapUIDPlus) {
		if err := s.c.UIDExpunge(s.uidSet(uids)).Close(); err != nil {
			return fmt.Errorf("uid expunge: %v", err)
		}
		return nil
	}
	if err := s.c.Expunge().Close(); err != nil {
		return fmt.Errorf("expunge: %v", err)
	}
	return nil
}

// supportsIdle reports whether the server advertises IDLE.
func (s *clientSession) supportsIdle() bool {
	_, ok := s.c.Caps()["IDLE"]
	return ok
}

// Idle enters IDLE on the mailbox, forwarding unilateral "new mail" updates
// to notify. go-imap restarts IDLE automatically every ~28 min (heartbeat),
// so a long-lived idle cannot be dropped by inactivity timeouts. The returned
// stop function ends IDLE and restores the session for commands.
func (s *clientSession) Idle(mailbox string, notify chan<- struct{}) (func(), error) {
	if !s.supportsIdle() {
		return nil, ErrIdleUnsupported
	}
	if _, err := s.Select(mailbox); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.notify = notify
	s.mu.Unlock()

	cmd, err := s.c.Idle()
	if err != nil {
		s.mu.Lock()
		s.notify = nil
		s.mu.Unlock()
		return nil, fmt.Errorf("idle su %q: %v", mailbox, err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Close() // sends DONE
			_ = cmd.Wait()
			s.mu.Lock()
			s.notify = nil
			s.mu.Unlock()
		})
	}
	return stop, nil
}

// Append delivers raw bytes into the mailbox (APPEND, UIDPLUS APPENDUID).
func (s *clientSession) Append(mailbox string, raw []byte, flags []string, date time.Time) (uint32, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("append: messaggio vuoto")
	}
	opts := &imap.AppendOptions{Time: date}
	for _, f := range flags {
		opts.Flags = append(opts.Flags, imap.Flag(f))
	}
	cmd := s.c.Append(mailbox, int64(len(raw)), opts)
	if _, err := bytes.NewReader(raw).WriteTo(cmd); err != nil {
		return 0, fmt.Errorf("append in %q: %v", mailbox, err)
	}
	if err := cmd.Close(); err != nil {
		return 0, fmt.Errorf("append in %q: %v", mailbox, err)
	}
	data, err := cmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("append in %q: %v", mailbox, err)
	}
	if data != nil {
		return uint32(data.UID), nil
	}
	return 0, nil
}