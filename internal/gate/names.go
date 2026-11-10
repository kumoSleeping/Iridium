package gate

import (
	"errors"
	"strings"
	"unicode"
)

// MaxNameLen is the longest name of an app or a login, in characters (runes).
const MaxNameLen = 12

// Name errors. Their Code is what the API sends to pages.
var (
	ErrNameEmpty    = &NameError{"empty"}
	ErrNameTooLong  = &NameError{"too_long"}
	ErrNameInvalid  = &NameError{"invalid"}
	ErrNameReserved = &NameError{"reserved"}
	ErrNameLetter   = &NameError{"letter"}
	ErrNameTaken    = &NameError{"taken"}
)

// NameError says why a name cannot be used.
type NameError struct{ Code string }

func (e *NameError) Error() string { return "gate: name " + strings.ReplaceAll(e.Code, "_", " ") }

// reservedNames are taken by what is built in: the host (主机, host; 本机 and local as it was
// called), the app ir_web, Iridium itself (shown as Ir) and unknown sources (未知), in both UI
// languages (docs/next/decisions.md 5).
var reservedNames = []string{"host", "主机", "本机", "local", "ir_web", "Ir", "未知", "unknown"}

// NormalizeName returns the stored form of a name, or why it cannot be one:
// full-width ASCII becomes ASCII, spaces are trimmed and collapsed, and what remains must
// be 1–12 letters, digits, marks, spaces or "-_.". Brackets, slashes, colons, the middle
// dot and the like are refused because names appear in source tags like [名字] and
// [QQ·张三] and in `ir` arguments like mac:~/dir and 应用/登录. A single ASCII letter is refused
// too: as a place's prefix it would read as a Windows drive (C:\dir).
func NormalizeName(raw string) (string, error) {
	var out []rune
	space := false
	for _, r := range raw {
		r = narrow(r)
		switch {
		case unicode.IsSpace(r):
			space = len(out) > 0
		case nameRune(r):
			if space {
				out = append(out, ' ')
				space = false
			}
			out = append(out, r)
		default:
			return "", ErrNameInvalid
		}
	}
	if len(out) == 0 {
		return "", ErrNameEmpty
	}
	if len(out) > MaxNameLen {
		return "", ErrNameTooLong
	}
	if len(out) == 1 && out[0] < 0x80 && unicode.IsLetter(out[0]) {
		return "", ErrNameLetter
	}
	name := string(out)
	for _, r := range reservedNames {
		if nameKey(r) == nameKey(name) {
			return "", ErrNameReserved
		}
	}
	return name, nil
}

func nameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '-' || r == '_' || r == '.'
}

// narrow maps full-width ASCII (as typed with a CJK input method) to ASCII.
func narrow(r rune) rune {
	switch {
	case r >= 0xFF01 && r <= 0xFF5E:
		return r - 0xFF01 + 0x21
	case r == 0x3000:
		return ' '
	}
	return r
}

// nameKey is the case-insensitive identity of a normalized name: each rune replaced by the
// smallest rune of its simple case-folding orbit.
func nameKey(name string) string {
	return strings.Map(func(r rune) rune {
		min := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < min {
				min = f
			}
		}
		return min
	}, name)
}

// nameCode is the API code for a name error, or "" for nil and other errors.
func nameCode(err error) string {
	var ne *NameError
	if errors.As(err, &ne) {
		return ne.Code
	}
	return ""
}

// NameCode is the reason code of a name error ("" if err is none).
func NameCode(err error) string { return nameCode(err) }
