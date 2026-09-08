package mime

// PGP/Inline and PGP/MIME detection for the ingest pipeline (M9). Parse is
// pure: it flags and captures armor; decryption/verification happen in the
// IPC layer where the keychain-backed keyring lives.

import (
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

const (
	armorBeginMessage = "-----BEGIN PGP MESSAGE-----"
	armorEndMessage   = "-----END PGP MESSAGE-----"
)

// extractBlock returns the complete armored block starting with begin and
// ending with end (inclusive). Empty when absent or unterminated.
func extractBlock(s, begin, end string) string {
	i := strings.Index(s, begin)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], end)
	if j < 0 {
		return ""
	}
	return s[i : i+j+len(end)]
}

// PGPInfo is what detection pulls out of a message text.
type PGPInfo struct {
	EncryptedPayload string // armored PGP MESSAGE block (empty if none)
	Clearsigned      string // complete clearsigned block (empty if none)
	Plaintext        string // readable plaintext of the clearsigned block
	IsSigned         bool
	IsEncrypted      bool
}

// DetectPGP scans message text for PGP/Inline armor. Encrypted wins over
// signed (signed+encrypted PGP/Inline is rare; encryption hides the text).
func DetectPGP(text string) PGPInfo {
	var info PGPInfo
	if enc := extractBlock(text, armorBeginMessage, armorEndMessage); enc != "" {
		info.EncryptedPayload = enc
		info.IsEncrypted = true
		return info
	}
	if strings.Contains(text, "-----BEGIN PGP SIGNED MESSAGE-----") {
		if block, _ := clearsign.Decode([]byte(text)); block != nil {
			info.Clearsigned = text
			info.Plaintext = string(block.Plaintext)
			info.IsSigned = true
		}
	}
	return info
}
