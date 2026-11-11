package chaos

import "strings"

// words is the vocabulary used to fill responses. Plain English so the output
// stays readable in a chat client during a campaign.
var words = strings.Fields(`the a an and or but so yet nor for of to in on at by with
	model response request token gateway latency load stream error retry
	quota budget organization user provider pipeline plugin channel
	measure threshold percentile throughput connection delay buffer event log
	calm fast slow stable fragile random repeated partial complete empty
	analyzes generates returns cuts waits restarts measures counts observes traces`)

// Text produces n words of filler, drawn with r, with a full stop every
// dozen words so the result looks like prose.
func Text(r *Rand, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			if i%12 == 0 {
				b.WriteString(". ")
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteString(words[r.Intn(len(words))])
	}
	b.WriteByte('.')
	return b.String()
}

// Words splits text into the pieces streamed one per chunk, each carrying its
// leading separator so concatenating them gives text back.
func Words(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 1; i < len(text); i++ {
		if text[i] == ' ' {
			out = append(out, text[start:i])
			start = i
		}
	}
	return append(out, text[start:])
}
