package main

import (
	"regexp"
	"strings"
)

// UG's mobile API returns tab/chord content as a single string using
// inline markup, e.g.:
//
//   [ch]G[/ch]Swing low, sweet [ch]C[/ch]chariot
//   [tab] ... [/tab]   (wraps tab/ASCII sections)
//
// This parser strips the wrapper tags and produces a line-by-line
// structure with chord symbols annotated by character offset, so the
// watch can render "chords above lyrics" and know exactly where each
// chord falls for auto-scroll highlighting.

var chordTagRe = regexp.MustCompile(`\[ch\](.*?)\[/ch\]`)
var otherTagRe = regexp.MustCompile(`\[/?(tab|div|span)[^\]]*\]`)

// ChordPosition marks where a chord symbol falls in a lyric line.
type ChordPosition struct {
	Symbol string `json:"symbol"`
	Offset int    `json:"offset"` // character index into Lyrics where the chord sits
}

// Line is one line of the chord chart: the lyric/text content, plus
// any chords positioned above it. A pure instrumental/chord-only line
// will have empty Lyrics and only Chords.
type Line struct {
	Lyrics string          `json:"lyrics"`
	Chords []ChordPosition `json:"chords"`
}

// ParseTabContent converts UG's raw markup string into structured lines.
func ParseTabContent(raw string) []Line {
	// Strip [tab]/[div] wrapper tags but keep their inner content.
	cleaned := otherTagRe.ReplaceAllString(raw, "")

	rawLines := strings.Split(cleaned, "\n")
	lines := make([]Line, 0, len(rawLines))

	for _, rl := range rawLines {
		var chords []ChordPosition
		// Walk the line, extracting [ch]X[/ch] tags and recording the
		// offset each chord would land at in the tag-stripped text.
		var b strings.Builder
		remaining := rl
		for {
			loc := chordTagRe.FindStringSubmatchIndex(remaining)
			if loc == nil {
				b.WriteString(remaining)
				break
			}
			b.WriteString(remaining[:loc[0]])
			symbol := remaining[loc[2]:loc[3]]
			chords = append(chords, ChordPosition{
				Symbol: symbol,
				Offset: b.Len(),
			})
			remaining = remaining[loc[1]:]
		}
		lines = append(lines, Line{
			Lyrics: b.String(),
			Chords: chords,
		})
	}

	return lines
}
