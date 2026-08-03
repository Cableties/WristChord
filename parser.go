package main

import (
	"regexp"
	"strings"
	"unicode"
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
		chords := make([]ChordPosition, 0)
		// Walk the line, extracting [ch]X[/ch] tags and recording the
		// offset each chord would land at in the tag-stripped text.
		//
		// visualCol tracks the true monospace column position, separate
		// from `b` (which only accumulates real, non-tag text for the
		// output Lyrics field — deliberately excluding chord symbols, so a
		// chord-only line's Lyrics stays blank/whitespace and is still
		// detected as chord-only downstream).
		//
		// Critically, visualCol must still advance by each chord symbol's
		// own character length after placing it — on a chord-only line
		// like "[ch]Em7[/ch]      [ch]G[/ch]", "Em7" occupies 3 real
		// columns in the original monospace layout even though its tag
		// gets stripped out of `b` entirely. Without this, every chord
		// after the first on such a line was undercounted by the combined
		// length of every chord before it, landing offsets several
		// characters too early (e.g. "G" landing on "is" instead of
		// "gonna").
		var b strings.Builder
		visualCol := 0
		remaining := rl
		for {
			loc := chordTagRe.FindStringSubmatchIndex(remaining)
			if loc == nil {
				b.WriteString(remaining)
				break
			}
			preText := remaining[:loc[0]]
			b.WriteString(preText)
			visualCol += len(preText)
			symbol := remaining[loc[2]:loc[3]]
			chords = append(chords, ChordPosition{
				Symbol: symbol,
				Offset: visualCol,
			})
			visualCol += len(symbol)
			remaining = remaining[loc[1]:]
		}
		lines = append(lines, Line{
			Lyrics: b.String(),
			Chords: chords,
		})
	}

	return splitRunOnLines(mergeChordAndLyricPairs(lines))
}

// splitRunOnLines handles a data-quality issue in some Ultimate Guitar
// contributions (not a scraping/parsing bug on our side): the raw content
// for a section is submitted as one continuous run-on string with no
// line-break markers between what should be separate printed lines at
// all — e.g. "...be the day" directly touching "That they're gonna..."
// with zero characters between them, not even a space.
//
// This is reliably detectable: an uppercase letter immediately preceded
// by a lowercase letter or sentence-ending punctuation (",.!?'"), with no
// whitespace at all in between, never happens in normal English prose —
// a real sentence boundary always has a space there. Wherever that exact
// pattern appears, a line-break character was almost certainly stripped
// down to nothing upstream, so this reconstructs it as a real line split,
// carrying each chord into whichever new segment it now falls within and
// re-offsetting it relative to that segment's own start.
func splitRunOnLines(lines []Line) []Line {
	result := make([]Line, 0, len(lines))
	for _, l := range lines {
		result = append(result, splitRunOnSentences(l)...)
	}
	return result
}

func splitRunOnSentences(line Line) []Line {
	runes := []rune(line.Lyrics)
	if len(runes) == 0 {
		return []Line{line}
	}

	var splitPoints []int
	for i := 1; i < len(runes); i++ {
		curr := runes[i]
		if !unicode.IsUpper(curr) {
			continue
		}
		prev := runes[i-1]
		if unicode.IsLower(prev) || strings.ContainsRune(",.!?'", prev) {
			splitPoints = append(splitPoints, i)
		}
	}
	if len(splitPoints) == 0 {
		return []Line{line}
	}

	boundaries := append([]int{0}, splitPoints...)
	boundaries = append(boundaries, len(runes))

	segments := make([]Line, 0, len(boundaries)-1)
	for k := 0; k < len(boundaries)-1; k++ {
		start, end := boundaries[k], boundaries[k+1]
		isLastSegment := k == len(boundaries)-2
		segChords := make([]ChordPosition, 0)
		for _, c := range line.Chords {
			// A trailing chord marker with no lyric text after it (e.g.
			// "...love.....I'm in love" where a chord's offset lands
			// exactly at the end of the combined string) has an offset
			// equal to the full string length. The strict c.Offset < end
			// check excluded that chord from every segment entirely,
			// since it fails "< end" for the last segment too — silently
			// dropping it rather than keeping it at the tail end of
			// whichever segment it belongs to. The last segment accepts
			// anything >= start with no upper bound.
			belongsHere := c.Offset >= start && (c.Offset < end || isLastSegment)
			if belongsHere {
				segChords = append(segChords, ChordPosition{
					Symbol: c.Symbol,
					Offset: coerceIntoRange(c.Offset-start, end-start),
				})
			}
		}
		segments = append(segments, Line{
			Lyrics: string(runes[start:end]),
			Chords: segChords,
		})
	}
	return segments
}

// coerceIntoRange clamps an offset so it never exceeds a segment's own
// text length — needed for the trailing-chord case above, where the
// chord's original offset can sit past the end of the (now much shorter)
// final segment once the line has been split.
func coerceIntoRange(offset int, maxLen int) int {
	if offset > maxLen {
		return maxLen
	}
	return offset
}

// mergeChordAndLyricPairs handles Ultimate Guitar's other common chord
// chart convention, distinct from inline [ch]word[/ch] tagging: a line
// containing only chord symbols (rest is whitespace), immediately followed
// by a separate line containing the actual lyric text with no chord tags
// at all. Left unmerged, these arrive as two unrelated Line entries — a
// chord row with a blank Lyrics field, then a lyric row with no chords —
// which is why chords would render as their own detached line instead of
// positioned above the words they actually belong to.
//
// This only merges exactly one chord-only line with the one lyric line
// immediately after it. An earlier version tried to keep consuming an
// entire "run" of alternating chord/lyric segments, to handle a single
// printed line with multiple chord changes represented as several small
// pairs — but that generalization couldn't distinguish a genuine
// continuation from two separate printed lines that each simply happen to
// be their own chord+lyric pair back to back, and ended up gluing
// unrelated lines together (e.g. "...I do" + "About you now." with zero
// space between them), which then tripped the run-on-line splitter and
// lost chords in the process. Handling only one pair at a time is less
// clever but safe — it may occasionally leave a genuinely multi-segment
// mid-sentence chord change only partially merged, but that's a much
// smaller and more visible problem than silently dropping chords.
func mergeChordAndLyricPairs(lines []Line) []Line {
	merged := make([]Line, 0, len(lines))
	i := 0
	for i < len(lines) {
		current := lines[i]
		isChordOnly := len(current.Chords) > 0 && strings.TrimSpace(current.Lyrics) == ""

		if isChordOnly && i+1 < len(lines) {
			next := lines[i+1]
			isPlainLyric := len(next.Chords) == 0 && strings.TrimSpace(next.Lyrics) != ""
			if isPlainLyric {
				merged = append(merged, Line{
					Lyrics: strings.TrimRight(next.Lyrics, "\r\n"),
					Chords: current.Chords,
				})
				i += 2
				continue
			}
		}

		merged = append(merged, current)
		i++
	}
	return merged
}
