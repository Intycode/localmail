package mime

// Anti-tracking (M6): email HTML bodies are sanitized ONCE at ingest time, so
// what is stored locally (and searched, quoted, rendered) is already clean.
//
//   - tracking pixels are removed permanently: 1x1/0x0 images, images hidden
//     with CSS, and src hosts in a known-tracker blocklist;
//   - remaining <img src="http(s)://…"> are counted (message.RemoteImages) so
//     the UI can gate them behind explicit consent;
//   - <a href> URLs lose tracking query parameters (utm_*, fbclid, …);
//     functional parameters (unsubscribe tokens and the like) are preserved;
//   - <script> is dropped (defense in depth: the webview sanitizer already
//     strips it at render time).
//
// The HTML is re-serialized from the parse tree, which also normalizes
// malformed markup mail clients emit in the wild.

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// trackerHosts are hosts that (almost) exclusively serve tracking pixels.
// Matching is exact on the host or on the ".<suffix>" of the host.
var trackerHosts = map[string]struct{}{
	"click.mailchimp.com":            {},
	"email.mailchimp.com":            {},
	"lists.mailchimp.com":            {},
	"mail.zoho.com":                  {},
	"clicks.mail.com":                {},
	"track.createsend.com":           {},
	"email.createsend.com":           {},
	"mailgun.org":                    {}, // suffix: email.mailgun.org
	"click.messagegears.com":         {},
	"tr.klaviyomail.com":             {},
	"email.klaviyomail.com":          {},
	"open.convertkit-mail.com":       {},
	"click.convertkit-mail.com":      {},
	"mail.feeditback.com":            {},
	"pixel.app.returnpath.net":       {},
	"click.qq.mail.ru":               {},
	"notify.boltsmtp.com":            {},
	"links.email.mobylla.com":        {},
	"pixel.monkeywrench.r7.com":      {},
	"no-reply.mailservers.net":       {},
	"click1.mailservers.net":         {},
	"mailthem.prfct.co":              {},
	"t.urllab.com":                   {},
	"open.spoteo.com":                {},
	"link.frdsp.com":                 {},
	"e.dropboxmail.com":              {},
	"u4489131.ct.sendgrid.net":       {},
	"u5233726.ct.sendgrid.net":       {},
	"mailstat.it":                    {},
	"track.mailerlite.com":           {},
	"pixel.mailgun.net":              {},
	"emails.mailjet.com":             {},
	"m.mailjet.com":                  {},
	"t.email.otto.de":                {},
	"newsletters.mailsfun.com":       {},
}

// trackingParams are URL query parameters used to report link clicks back to
// the sender. Deliberately conservative: functional tokens (unsubscribe, ids
// the destination needs) are NOT here.
var trackingParams = map[string]struct{}{
	"utm_source":      {},
	"utm_medium":      {},
	"utm_campaign":    {},
	"utm_term":        {},
	"utm_content":     {},
	"utm_id":          {},
	"utm_reader":      {},
	"utm_referrer":    {},
	"utm_social":      {},
	"utm_name":        {},
	"utm_cid":         {},
	"utm_creative":    {},
	"utm_keyword":     {},
	"fbclid":          {},
	"gclid":           {},
	"gclsrc":          {},
	"dclid":           {},
	"gbraid":          {},
	"wbraid":          {},
	"msclkid":         {},
	"twclid":          {},
	"yclid":           {},
	"mc_eid":          {},
	"mc_cid":          {},
	"_hsenc":          {},
	"_hsmi":           {},
	"vero_id":         {},
	"wickedid":        {},
	"trk_contact":     {},
	"trk_msg":         {},
	"trk_module":      {},
	"trk_sid":         {},
	"igshid":          {},
	"ref_src":         {},
	"ref_url":         {},
	"ref":             {},
	"spm":             {},
	"scm":             {},
	"snr":             {},
	"pk_campaign":     {},
	"pk_kwd":          {},
	"mtm_campaign":    {},
	"mtm_source":      {},
	"mtm_medium":      {},
	"mtm_keyword":     {},
	"ck_campaign":     {},
	"s_kwcid":         {},
	"elqtrackid":      {},
	"elqaid":          {},
	"elqcampaignid":   {},
	"_openstat":       {},
	"action_object_map": {},
	"action_type_map":   {},
	"action_ref_map":    {},
}

// isTrackerHost reports whether the host (or any registrar-style suffix of
// it) is a known pixel host.
func isTrackerHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	if _, ok := trackerHosts[host]; ok {
		return true
	}
	// suffix match for the registry-style entries ("mailgun.org")
	for i := strings.Index(host, "."); i >= 0; {
		if _, ok := trackerHosts[host[i+1:]]; ok {
			return true
		}
		j := strings.Index(host[i+1:], ".")
		if j < 0 {
			break
		}
		i = i + 1 + j
	}
	return false
}

// tinyImage reports whether the img tag looks like a tracking pixel: nominal
// size 0/1 in either dimension (attrs or inline style) or hidden with CSS.
func tinyImage(n *html.Node) bool {
	width := strings.TrimSpace(attr(n, "width"))
	height := strings.TrimSpace(attr(n, "height"))
	style := strings.ToLower(attr(n, "style"))
	if dimIsTiny(width) || dimIsTiny(height) {
		return true
	}
	for _, prop := range []string{"width", "height"} {
		if i := strings.Index(style, prop + ":"); i >= 0 {
			v := strings.TrimSpace(style[i+len(prop)+1:])
			v = strings.SplitN(v, ";", 2)[0]
			if dimIsTiny(strings.TrimSpace(v)) {
				return true
			}
		}
	}
	if strings.Contains(style, "display:none") || strings.Contains(style, "display: none") ||
		strings.Contains(style, "visibility:hidden") || strings.Contains(style, "visibility: hidden") ||
		strings.Contains(style, "opacity:0") || strings.Contains(style, "opacity: 0") {
		return true
	}
	if attr(n, "hidden") != "" {
		return true
	}
	return false
}

func dimIsTiny(v string) bool {
	v = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	return v == "0" || v == "1" || v == "0.5" || v == ".5"
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if !strings.EqualFold(a.Key, key) {
			out = append(out, a)
		}
	}
	n.Attr = out
}

// stripTrackingParams removes tracking query parameters from a URL string.
// Unparseable URLs are returned untouched.
func stripTrackingParams(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Query() == nil {
		return raw
	}
	q := u.Query()
	changed := false
	for key := range q {
		if _, track := trackingParams[strings.ToLower(key)]; track {
			q.Del(key)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	if len(q) == 0 {
		u.RawQuery = ""
	} else {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// renderChildren serializes the children of body back to an HTML fragment.
func renderChildren(body *html.Node) (string, error) {
	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&sb, c); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

// SanitizeHTML removes tracking artifacts from an email HTML body and counts
// the remote images that remain (the ones the UI gates behind consent).
func SanitizeHTML(body string) (string, int) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return body, 0 // unparseable: render-side sanitizer still blocks it
	}
	var findBody func(*html.Node) *html.Node
	findBody = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.DataAtom == atom.Body {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if b := findBody(c); b != nil {
				return b
			}
		}
		return nil
	}
	root := findBody(doc)
	if root == nil {
		return body, 0
	}

	remoteImages := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		// iterate with care: children may be removed mid-loop
		child := n.FirstChild
		for child != nil {
			next := child.NextSibling
			walk(child)
			child = next
		}
		if n.Type != html.ElementNode {
			return
		}
		switch n.DataAtom {
		case atom.Script, atom.Track:
			if n.Parent != nil {
				n.Parent.RemoveChild(n)
			}
		case atom.Img:
			src := strings.TrimSpace(attr(n, "src"))
			if tinyImage(n) {
				if n.Parent != nil {
					n.Parent.RemoveChild(n)
				}
				return
			}
			lower := strings.ToLower(src)
			if strings.HasPrefix(lower, "data:") && strings.Contains(lower, "image/svg") {
				// remote-behaving vector images: count like remote content
				remoteImages++
				return
			}
			if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
				return // cid:, data: (inline raster), relative: not remote
			}
			u, err := url.Parse(src)
			if err != nil || isTrackerHost(u.Hostname()) {
				if n.Parent != nil {
					n.Parent.RemoveChild(n)
				}
				return
			}
			remoteImages++
			// tracking attributes served by click-and-open reporters
			removeAttr(n, "srcset")
		case atom.A:
			if href := attr(n, "href"); href != "" {
				lower := strings.ToLower(href)
				if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
					if cleaned := stripTrackingParams(href); cleaned != href {
						setAttr(n, "href", cleaned)
					}
				}
			}
		case atom.Base:
			if n.Parent != nil {
				n.Parent.RemoveChild(n)
			}
		}
	}
	walk(root)

	clean, err := renderChildren(root)
	if err != nil {
		return body, remoteImages
	}
	return clean, remoteImages
}
