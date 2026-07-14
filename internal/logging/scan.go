package logging

import (
	"bufio"
	"encoding/json"
	"os"
)

// ScanResult summarizes a tolerant scan of an events.jsonl file.
type ScanResult struct {
	Count        int
	HashChainOK  bool
	HashError    string
	TruncatedEnd bool
}

// ScanEvents reads an events.jsonl file, verifying the hash chain when present.
// It tolerates a truncated final line (crash recovery): a trailing partial or
// unparseable line is reported via TruncatedEnd rather than failing the scan
// (GO_AGENT_PLAN.md §9.5, §14.4).
func ScanEvents(path string) (ScanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return ScanResult{}, err
	}
	defer f.Close()

	res := ScanResult{HashChainOK: true}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024), 64<<20)
	var prevHash string
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			// Partial trailing line from a crash: stop, mark truncated.
			res.TruncatedEnd = true
			break
		}
		res.Count++
		if env.Hash != "" {
			// Verify the chain link and recompute the event's own hash so that
			// tampering with any field is detected, not just a broken link.
			if env.PrevHash != prevHash && res.HashError == "" {
				res.HashChainOK = false
				res.HashError = "prev_hash mismatch at sequence " + itoa(env.Sequence)
			}
			if want, herr := hashEnvelope(env); herr == nil && want != env.Hash && res.HashError == "" {
				res.HashChainOK = false
				res.HashError = "content hash mismatch at sequence " + itoa(env.Sequence)
			}
			prevHash = env.Hash
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	return res, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
