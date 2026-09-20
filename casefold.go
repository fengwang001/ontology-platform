package ontology

// fullFoldTable maps a rune to its Unicode full case folding when
// that folding expands to a different (usually multi-rune)
// sequence — the "F" mappings of CaseFolding.txt plus the Turkic
// mapping for U+0130. Runes absent from the table use simple
// folding instead. All values are written with explicit escapes so
// the exact code points are unambiguous.
//
// Data: https://www.unicode.org/Public/UCD/latest/ucd/CaseFolding.txt
var fullFoldTable = map[rune]string{
	0x00DF: "ss",  // ß LATIN SMALL LETTER SHARP S
	0x0130: "i̇",  // İ (i + combining dot above)
	0x0149: "ʼn",  // ŉ
	0x01F0: "ǰ",  // ǰ
	0x0390: "ΐ", // ΐ
	0x03B0: "ΰ", // ΰ
	0x0587: "եւ",  // ARMENIAN ech yiwn
	0x1E96: "ẖ",  // h with line below
	0x1E97: "ẗ",  // t with diaeresis
	0x1E98: "ẘ",  // w with ring above
	0x1E99: "ẙ",  // y with ring above
	0x1E9A: "aʾ",  // a with right half ring
	0x1E9E: "ss",  // ẞ LATIN CAPITAL LETTER SHARP S
	0x1F50: "ὐ",  // upsilon with psili
	0x1F52: "ὒ", // upsilon psili varia
	0x1F54: "ὔ", // upsilon psili oxia
	0x1F56: "ὖ", // upsilon psili perispomeni
	0x1FB2: "ὰι",  // varia + ypogegrammeni
	0x1FB3: "αι",  // alpha + ypogegrammeni
	0x1FB4: "άι",  // oxia + ypogegrammeni
	0x1FB6: "ᾶ",  // alpha + perispomeni
	0x1FB7: "ᾶι", // alpha perispomeni ypogegrammeni
	0x1FBC: "αι",  // ALPHA + prosgegrammeni
	0x1FBE: "ι",   // GREEK PROSGEGRAMMENI
	0x1FC2: "ὴι",  // eta varia ypogegrammeni
	0x1FC3: "ηι",  // eta + ypogegrammeni
	0x1FC4: "ήι",  // eta oxia ypogegrammeni
	0x1FC6: "ῆ",  // eta + perispomeni
	0x1FC7: "ῆι", // eta perispomeni ypogegrammeni
	0x1FCC: "ηι",  // ETA + prosgegrammeni
	0x1FD2: "ῒ", // iota dialytika varia
	0x1FD3: "ΐ", // iota dialytika oxia
	0x1FD6: "ῖ",  // iota + perispomeni
	0x1FD7: "ῗ", // iota dialytika perispomeni
	0x1FE2: "ῢ", // upsilon dialytika varia
	0x1FE3: "ΰ", // upsilon dialytika oxia
	0x1FE4: "ῤ",  // rho with psili
	0x1FE6: "ῦ",  // upsilon + perispomeni
	0x1FE7: "ῧ", // upsilon dialytika perispomeni
	0x1FF2: "ὼι",  // omega varia ypogegrammeni
	0x1FF3: "ωι",  // omega + ypogegrammeni
	0x1FF4: "ώι",  // omega oxia ypogegrammeni
	0x1FF6: "ῶ",  // omega + perispomeni
	0x1FF7: "ῶι", // omega perispomeni ypogegrammeni
	0x1FFC: "ωι",  // OMEGA + prosgegrammeni
	0xFB00: "ff",  // ﬀ ligature
	0xFB01: "fi",  // ﬁ ligature
	0xFB02: "fl",  // ﬂ ligature
	0xFB03: "ffi", // ﬃ ligature
	0xFB04: "ffl", // ﬄ ligature
	0xFB05: "st",  // ﬅ ligature
	0xFB06: "st",  // ﬆ ligature
	0xFB13: "մն",  // ARMENIAN men now
	0xFB14: "մե",  // ARMENIAN men ech
	0xFB15: "մի",  // ARMENIAN men ini
	0xFB16: "վն",  // ARMENIAN vew now
	0xFB17: "մխ",  // ARMENIAN men xeh
}

// init covers the Greek ypogegrammeni series U+1F80..U+1FAF: each
// rune folds to the matching rune 0x80 lower plus U+03B9 (iota).
func init() {
	for base := rune(0x1F00); base <= 0x1F38; base += 8 {
		for i := rune(0); i < 8; i++ {
			fullFoldTable[base+0x80+i] = string(base+i) + "ι"
		}
	}
}

// fullFold returns the full case folding of r, if it has a special
// multi-rune (or otherwise non-simple) folding.
func fullFold(r rune) (string, bool) {
	s, ok := fullFoldTable[r]
	return s, ok
}
