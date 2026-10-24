package download

import (
	"strings"
	"unicode"
)

// The PDF's core fonts (Times, Courier) draw cp1252 (Windows-1252): ASCII,
// Latin-1 and the 27 characters below at 0x80–0x9F, which include the
// typographer's dashes, curly quotes, ellipsis and bullet. asCP1252 replaces
// every other character with a stand-in from that set, so the cp1252
// translator never meets a character it lacks (it would draw a '.').

// cp1252Extra are the characters cp1252 has at 0x80–0x9F.
const cp1252Extra = "€‚ƒ„…†‡ˆ‰Š‹ŒŽ‘’“”•–—˜™š›œžŸ"

// softHyphen marks where a word may break; it is never drawn.
const softHyphen = '\u00AD'

// inCP1252 reports whether cp1252 has r.
func inCP1252(r rune) bool {
	return r < 0x80 || r >= 0xA0 && r <= 0xFF || strings.ContainsRune(cp1252Extra, r)
}

// latinExtA is the base letter of each character in Latin Extended-A
// (U+0100–U+017F): Ā→A, ł→l, ő→o. cp1252 has Œ œ Š š Ÿ Ž ž itself, so their
// entries are never used; Ĳ and ĳ have two letters (cp1252Fallback).
const latinExtA = "AaAaAaCcCcCcCcDd" + "DdEeEeEeEeEeGgGg" + "GgGgHhHhIiIiIiIi" +
	"IiIiJjKkkLlLlLlL" + "lLlNnNnNnnNnOoOo" + "OoOoRrRrRrSsSsSs" + "SsTtTtTtUuUuUuUu" +
	"UuUuWwYyYZzZzZzs"

// cp1252Fallback stands in for common characters cp1252 lacks: dashes and
// quotes it has a near twin of, arrows and comparison signs as ASCII, spaces
// as spaces (a narrow or figure no-break space stays unbreakable), box
// drawing as an ASCII tree, and a few Greek letters by name. An empty
// replacement drops the character.
var cp1252Fallback = map[rune]string{
	'\u0132': "IJ", '\u0133': "ij",
	'\u2010': "-", '\u2011': "-", '\u2012': "–", '\u2015': "—", '\u2043': "-", '\u2212': "-",
	'\u201B': "'", '\u201F': "\"", '\u2032': "'", '\u2033': "\"", '\u2035': "'",
	'\u2190': "<-", '\u2192': "->", '\u2191': "^", '\u2193': "v", '\u2194': "<->",
	'\u21D0': "<=", '\u21D2': "=>", '\u21D4': "<=>", '\u2197': "->", '\u21A9': "<-", '\u21B3': "->",
	'\u2264': "<=", '\u2265': ">=", '\u2260': "!=", '\u2248': "~", '\u2261': "==", '\u221E': "inf",
	'\u22EF': "…", '\u2219': "·", '\u2023': "•", '\u25CF': "•", '\u25E6': "o", '\u25AA': "•",
	'\u2713': "[x]", '\u2714': "[x]", '\u2705': "[x]", '\u2717': "[ ]", '\u2718': "[ ]", '\u274C': "[ ]",
	'\u2002': " ", '\u2003': " ", '\u2004': " ", '\u2005': " ", '\u2006': " ", '\u2008': " ",
	'\u2009': " ", '\u200A': " ", '\u205F': " ", '\u3000': " ",
	'\u2007': "\u00A0", '\u202F': "\u00A0",
	'\u200B': "", '\u200C': "", '\u200D': "", '\u2060': "", '\uFEFF': "",
	'\u2502': "|", '\u2503': "|", '\u2551': "|", '\u2500': "-", '\u2501': "-", '\u2550': "=",
	'\u251C': "|", '\u2523': "|", '\u2524': "|", '\u2514': "`", '\u2517': "`", '\u250C': "+", '\u2510': "+", '\u2518': "+",
	'\u252C': "+", '\u2534': "+", '\u253C': "+",
	'\u03B1': "alpha", '\u03B2': "beta", '\u03B3': "gamma", '\u03B4': "delta", '\u03B5': "epsilon", '\u03B8': "theta",
	'\u03BB': "lambda", '\u03BC': "µ", '\u03C0': "pi", '\u03C3': "sigma", '\u03C4': "tau", '\u03C6': "phi", '\u03C9': "omega",
	'\u0394': "Delta", '\u03A3': "Sigma", '\u03A9': "Omega",
}

// asCP1252 returns s with every character cp1252 lacks replaced by its
// stand-in (cp1252Fallback, then the base letter for Latin Extended-A);
// combining marks, emoji and other symbols are dropped, and anything else
// becomes '?'. A soft hyphen is dropped too: the core fonts would draw it.
func asCP1252(s string) string {
	clean := true
	for _, r := range s {
		if !inCP1252(r) || r == softHyphen {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == softHyphen {
			continue
		}
		if inCP1252(r) {
			b.WriteRune(r)
			continue
		}
		if fb, ok := cp1252Fallback[r]; ok {
			b.WriteString(fb)
			continue
		}
		switch {
		case r >= 0x100 && r <= 0x17F:
			b.WriteByte(latinExtA[r-0x100])
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.So, unicode.Sk, unicode.Cs, unicode.Co):
			// combining accents (already on their letter), format controls,
			// emoji and pictographs: nothing to draw
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}
