// Package assess implements the local spam/phishing heuristics (M7).
//
// Everything runs ON DEVICE at ingest time: no network calls, no learning
// with remote data, no user content leaves the machine. Evaluate inspects a
// parsed message (already HTML-sanitized by the M6 anti-tracking pass) and
// produces:
//
//   - a 0..100 SpamScore: weighted keyword/shape signals;
//   - Phishing: human-readable flags for masked links, IP/shortener/IDN
//     destinations, brand impersonation, login-bait wording, dangerous
//     attachments.
//
// High scores alone never move or delete a message: they surface as UI
// warnings. The user stays in control of what is spam.
package assess

import (
	"net/url"
	"regexp"
	"strings"

	"mail/backend/internal/domain"
)

// Result is the assessment outcome.
type Result struct {
	SpamScore float64
	Phishing  []string
}

// Thresholds the UI maps to badges.
const (
	// SpamThreshold: at or above this the message is considered spam.
	SpamThreshold = 60.0
	// SuspiciousThreshold: at or above this the UI shows a warning badge.
	SuspiciousThreshold = 35.0
)

// shorteners are URL shorteners commonly used to hide phishing destinations.
var shorteners = map[string]struct{}{
	"bit.ly": {}, "tinyurl.com": {}, "t.co": {}, "goo.gl": {}, "is.gd": {},
	"cutt.ly": {}, "rebrand.ly": {}, "ow.ly": {}, "buff.ly": {}, "shorturl.at": {},
	"rb.gy": {}, "t.ly": {}, "tiny.cc": {}, "shorte.st": {}, "lnkd.in": {},
}

// spammyTLDs are cheap registrar TLDs overrepresented in spam.
var spammyTLDs = map[string]struct{}{
	".top": {}, ".xyz": {}, ".click": {}, ".work": {}, ".fit": {},
	".rest": {}, ".icu": {}, ".cfd": {}, ".sbs": {},
}

// spamWords: weighted cliches (subject and body). Keys are lowercase.
var spamWords = map[string]float64{
	"viagra":                 25,
	"casino":                 18,
	"lotteria":               20,
	"lottery":                20,
	"hai vinto":              22,
	"you have won":           22,
	"congratulazioni":        12,
	"congratulations":        12,
	"premio istantaneo":      18,
	"guadagno facile":        22,
	"work from home":         16,
	"lavora da casa":         16,
	"investimento sicuro":    20,
	"rischio zero":           20,
	"rendimento garantito":   22,
	"credito facilitato":     16,
	"hai ricevuto una multa": 24,
	"rinnovo antivirus":      16,
	"rischi la chiusura":     20,
	"ora o mai":              16,
	"clicca qui":             12,
	"click here":             10,
	"clicca subito":          14,
	"ultima possibilita":     14,
	"ultime ore":             10,
	"offerta limitata":       14,
	"solo oggi":              10,
	"affrettati":             12,
	"vincitore":              14,
	"sconto esclusivo":       10,
	"100% gratis":            14,
}

// loginBait: phishing wording that asks for credential/confirm actions.
var loginBait = []string{
	"verifica il tuo account", "verify your account", "conferma il tuo account",
	"confirm your account", "account bloccato", "account locked", "account sospeso",
	"account suspended", "password scaduta", "password expired", "aggiorna i dati",
	"update your payment", "aggiorna il pagamento", "riattiva l'account",
	"conferma l'identita", "confirm your identity",
}

// brands: impersonation targets. For a hit, a link must point to a domain
// that does NOT contain the brand name to raise a flag.
var brands = []string{
	"paypal", "poste", "amazon", "apple", "google", "microsoft", "ebay",
	"netflix", "dhl", "gls", "bancoposta",
}

// dangerousExts: attachment extensions flagged as phishing-risk.
var dangerousExts = map[string]struct{}{
	".exe": {}, ".scr": {}, ".js": {}, ".vbs": {}, ".bat": {}, ".cmd": {},
	".hta": {}, ".iso": {}, ".lnk": {}, ".msi": {}, ".jar": {},
}

// Patterns declared as constants so the expressions stay readable.
const (
	IPHostPattern = "^\\d{1,3}(\\.\\d{1,3}){3}$"
	IDNPattern    = "(^|\\.)xn--"
	PunctPattern  = "!{3,}"
	DomainPattern = "([a-z0-9-]+\\.)+[a-z]{2,}"
	AnchorPattern = "(?is)<a\\s[^>]*href=\"([^\"]+)\"[^>]*>(.*?)</a>"
	TagPattern    = "<[^>]*>"
)

var (
	ipHostRe = regexp.MustCompile(IPHostPattern)
	idnRe    = regexp.MustCompile(IDNPattern)
	punctRe  = regexp.MustCompile(PunctPattern)
	domainRe = regexp.MustCompile(DomainPattern)
	anchorRe = regexp.MustCompile(AnchorPattern)
	tagRe    = regexp.MustCompile(TagPattern)
)

// Evaluate runs every heuristic on a parsed message. Safe on zero values.
func Evaluate(m *domain.Message) Result {
	var r Result
	subject := strings.ToLower(m.Subject)
	body := strings.ToLower(m.BodyText + " " + m.Body)
	hay := subject + "\n" + body

	// 1. weighted keywords
	for w, weight := range spamWords {
		if strings.Contains(hay, w) {
			r.SpamScore += weight
			if strings.Contains(subject, w) {
				r.SpamScore += weight / 2 // subject hits weigh more
			}
		}
	}
	if r.SpamScore > 45 {
		r.SpamScore = 45 // keyword cap: one class of signal cannot dominate
	}

	// 2. subject shape
	trimmed := strings.TrimSpace(strings.TrimRight(m.Subject, "!. \t"))
	if len(trimmed) >= 10 {
		letters := 0
		uppers := 0
		for _, c := range trimmed {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
				letters++
				if c >= 'A' && c <= 'Z' {
					uppers++
				}
			}
		}
		if letters >= 10 && float64(uppers)/float64(letters) > 0.75 {
			r.SpamScore += 10
		}
	}
	if punctRe.MatchString(m.Subject) {
		r.SpamScore += 6
	}

	// 3. sender domain reputation (cheap TLDs)
	fromDomain := domainOf(m.From.Email)
	for tld := range spammyTLDs {
		if strings.HasSuffix(fromDomain, tld) {
			r.SpamScore += 12
			break
		}
	}

	// 4. image-only newsletter (no readable text)
	if strings.TrimSpace(m.BodyText) == "" && m.Body != "" {
		r.SpamScore += 8
	}

	// 5. links analysis (on the sanitized HTML)
	links := extractLinks(m.Body)
	if len(links) > 10 {
		r.SpamScore += 6
	}
	seenFlags := map[string]struct{}{}
	for _, l := range links {
		u, err := url.Parse(l.href)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		add := func(flag string, weight float64) {
			if _, dup := seenFlags[flag]; dup {
				return
			}
			seenFlags[flag] = struct{}{}
			r.Phishing = append(r.Phishing, flag)
			r.SpamScore += weight
		}
		switch {
		case ipHostRe.MatchString(host):
			add("Link verso un indirizzo IP diretto ("+host+")", 18)
		case idnRe.MatchString(host):
			add("Link verso un dominio con caratteri codificati IDN ("+host+"): possibile homoglyph", 18)
		}
		if _, ok := shorteners[host]; ok {
			add("Link accorciato ("+host+"): destinazione nascosta", 12)
		}
		// masked link: the visible text shows a domain that is not the target
		if textHost := domainOf(l.text); textHost != "" {
			if !strings.HasSuffix(host, textHost) && !strings.HasSuffix(textHost, host) {
				add("Link mascherato: mostra "+textHost+" ma porta a "+host, 20)
			}
		}
		// brand impersonation: brand named in text/subject, foreign link domain
		for _, brand := range brands {
			if strings.Contains(l.text, brand) || strings.Contains(subject, brand) {
				if !strings.Contains(host, brand) {
					add("Riferimento a "+brand+" ma il link porta a "+host, 16)
					break
				}
			}
		}
	}
	// login bait wording paired with links (checked once, not per link)
	if len(links) > 0 {
		for _, bait := range loginBait {
			if strings.Contains(hay, bait) {
				r.Phishing = append(r.Phishing, "Richiesta di verifica/credenziali con link: possibile phishing")
				r.SpamScore += 14
				break
			}
		}
	}

	// 6. dangerous attachments
	for _, a := range m.Attachments {
		lower := strings.ToLower(a.Name)
		dot := strings.LastIndex(lower, ".")
		if dot >= 0 {
			if _, bad := dangerousExts[lower[dot:]]; bad {
				r.Phishing = append(r.Phishing, "Allegato potenzialmente pericoloso: "+a.Name)
				r.SpamScore += 22
				break
			}
		}
	}

	if r.SpamScore > 100 {
		r.SpamScore = 100
	}
	return r
}

// IsSpam reports whether the score reaches the spam threshold.
func (r Result) IsSpam() bool { return r.SpamScore >= SpamThreshold }

// IsSuspicious reports whether the UI should show a warning badge.
func (r Result) IsSuspicious() bool {
	return r.SpamScore >= SuspiciousThreshold || len(r.Phishing) > 0
}

type link struct {
	text string
	href string
}

// extractLinks pulls href/text pairs from the sanitized HTML. The M6
// sanitizer guarantees a normalized tree, so a regex over anchors is enough
// and keeps the package dependency-free.
func extractLinks(htmlBody string) []link {
	matches := anchorRe.FindAllStringSubmatch(htmlBody, -1)
	out := make([]link, 0, len(matches))
	for _, m := range matches {
		text := strings.TrimSpace(tagRe.ReplaceAllString(m[2], " "))
		text = strings.Join(strings.Fields(text), " ")
		out = append(out, link{text: text, href: m[1]})
	}
	return out
}

// domainOf extracts the lowercased domain part of an email address or URL.
// Returns "" when the input is not domain-like (used to skip link texts that
// are not domains).
func domainOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "mailto:")
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			s = u.Hostname()
		}
	}
	s = strings.TrimPrefix(s, "www.")
	if !domainRe.MatchString(s) || strings.ContainsAny(s, " \t") {
		return ""
	}
	return s
}

// Apply stores the assessment on the message.
func Apply(m *domain.Message) Result {
	r := Evaluate(m)
	m.SpamScore = r.SpamScore
	m.Phishing = r.Phishing
	return r
}
