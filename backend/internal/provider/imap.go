package provider

import (
	"fmt"
	"time"

	"mail/backend/internal/domain"
	"mail/backend/internal/email/imap"
	"mail/backend/internal/email/smtp"
)

// ImapProvider implements MailProvider over a generic IMAP server.
// It dials lazily and reuses one authenticated session across folders of the
// same account sync run; Close releases it.
type ImapProvider struct {
	cfg      imap.Config
	password string // runtime secret, never persisted
	smtpCfg  *smtp.Config // optional explicit outgoing config; derived when nil

	session imap.Session // lazily dialed
}

// NewImap builds an IMAP provider for one account. The password is a runtime
// secret delivered by Tauri: it stays in memory and is never persisted.
func NewImap(cfg imap.Config, password string) *ImapProvider {
	return &ImapProvider{cfg: cfg, password: password}
}

func (p *ImapProvider) sessionFor() (imap.Session, error) {
	if p.session != nil {
		return p.session, nil
	}
	s, err := imap.Dial(p.cfg, p.password)
	if err != nil {
		return nil, err
	}
	p.session = s
	return s, nil
}

// WithSMTP sets an explicit outgoing SMTP configuration (accounts with a
// configured "Server in uscita"). Without it the send derives the default
// from the IMAP config.
func (p *ImapProvider) WithSMTP(cfg smtp.Config) *ImapProvider {
	p.smtpCfg = &cfg
	return p
}

// Close releases the underlying IMAP session, if any.
func (p *ImapProvider) Close() {
	if p.session != nil {
		_ = p.session.Close()
		p.session = nil
	}
}

// SyncFolder pulls one folder. First sync (LastUID == 0 or UIDVALIDITY
// change) fetches the newest InitialWindow messages; afterwards only UIDs
// greater than ref.LastUID are fetched.
func (p *ImapProvider) SyncFolder(ref FolderRef) (FolderSync, error) {
	s, err := p.sessionFor()
	if err != nil {
		return FolderSync{}, err
	}

	state, err := s.Select(ref.MailboxName)
	if err != nil {
		return FolderSync{}, err
	}

	all, err := s.UIDs()
	if err != nil {
		return FolderSync{}, err
	}

	initial := ref.LastUID == 0 || (ref.UIDValidity != 0 && state.UIDValidity != ref.UIDValidity)

	var targets []uint32
	if initial {
		// newest N UIDs (list is ascending)
		if len(all) > imap.InitialWindow {
			all = all[len(all)-imap.InitialWindow:]
		}
		targets = all
	} else {
		for _, u := range all {
			if u > ref.LastUID {
				targets = append(targets, u)
			}
		}
	}

	fetched, err := s.Fetch(targets)
	if err != nil {
		return FolderSync{}, err
	}
	msgs := make([]RawMessage, 0, len(fetched))
	for _, m := range fetched {
		msgs = append(msgs, RawMessage{UID: m.UID, Flags: m.Flags, Data: m.Data})
	}

	return FolderSync{
		UIDValidity: state.UIDValidity,
		Messages:    msgs,
		AllUIDs:     all,
	}, nil
}

func (p *ImapProvider) selectAndDo(mailbox string, fn func(s imap.Session) error) error {
	s, err := p.sessionFor()
	if err != nil {
		return err
	}
	if _, err := s.Select(mailbox); err != nil {
		return err
	}
	return fn(s)
}

func (p *ImapProvider) MarkRead(mailbox string, uids []uint32) error {
	return p.selectAndDo(mailbox, func(s imap.Session) error { return s.MarkSeen(uids) })
}

// MarkUnread clears \Seen on the server (STORE -FLAGS (\Seen)).
func (p *ImapProvider) MarkUnread(mailbox string, uids []uint32) error {
	return p.selectAndDo(mailbox, func(s imap.Session) error { return s.MarkUnseen(uids) })
}

func (p *ImapProvider) Move(mailbox string, uids []uint32, target string) error {
	return p.selectAndDo(mailbox, func(s imap.Session) error { return s.MoveTo(uids, target) })
}

func (p *ImapProvider) Delete(mailbox string, uids []uint32) error {
	return p.selectAndDo(mailbox, func(s imap.Session) error { return s.Delete(uids) })
}

// Send delivers the message via SMTP (explicit config or the default derived
// from IMAP) and returns the raw delivered bytes.
func (p *ImapProvider) Send(from domain.Recipient, msg domain.ComposeMessage) ([]byte, error) {
	cfg := p.smtpCfg
	if cfg == nil {
		d := smtp.DefaultFor(p.cfg.Host, from.Email)
		cfg = &d
	}
	if cfg.Username == "" {
		cfg.Username = from.Email
	}
	// plaintext was gated at account-configuration time (explicit opt-in)
	return smtp.SendMessage(*cfg, p.password, cfg.Security == smtp.SecurityPlain, from, msg)
}

// Append delivers raw bytes into a mailbox via the IMAP session (APPEND).
func (p *ImapProvider) Append(mailbox string, raw []byte, flags []string, date time.Time) (uint32, error) {
	s, err := p.sessionFor()
	if err != nil {
		return 0, err
	}
	return s.Append(mailbox, raw, flags, date)
}

// Idle enters push mode on the mailbox (IMAP IDLE).
func (p *ImapProvider) Idle(mailbox string, notify chan<- struct{}) (func(), error) {
	s, err := p.sessionFor()
	if err != nil {
		return nil, err
	}
	return s.Idle(mailbox, notify)
}

// FetchRaw downloads one message's raw body (BODY[]) from the mailbox.
func (p *ImapProvider) FetchRaw(mailbox string, uid uint32) ([]byte, error) {
	var out []byte
	err := p.selectAndDo(mailbox, func(s imap.Session) error {
		raws, err := s.Fetch([]uint32{uid})
		if err != nil {
			return fmt.Errorf("fetch raw %d: %w", uid, err)
		}
		if len(raws) == 0 || len(raws[0].Data) == 0 {
			return fmt.Errorf("messaggio %d non trovato", uid)
		}
		out = raws[0].Data
		return nil
	})
	return out, err
}

// Ping validates the credentials with a cheap dial + login.
func (p *ImapProvider) Ping() error {
	_, err := p.sessionFor()
	return err
}

// Folders lists the mailboxes of the account (dials if needed).
func (p *ImapProvider) Folders() ([]imap.FolderInfo, error) {
	s, err := p.sessionFor()
	if err != nil {
		return nil, err
	}
	return s.Folders()
}