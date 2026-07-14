package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ahhh/fury_agent/internal/agent"
	"github.com/ahhh/fury_agent/internal/logging"
)

// cmdReplayLog validates and summarizes a session log directory. It does not
// re-execute anything (GO_AGENT_PLAN.md §11).
func cmdReplayLog(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: agent replay-log <session-dir>")
		return agent.ExitBadConfig
	}
	dir := args[0]
	turns := filepath.Join(dir, "turns.jsonl")

	scan, err := logging.ScanEvents(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "events:", err)
		return agent.ExitBadConfig
	}
	turnCount, err := countLines(turns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "turns:", err)
		return agent.ExitBadConfig
	}

	summary := map[string]any{
		"session_dir":   dir,
		"events":        scan.Count,
		"turns":         turnCount,
		"hash_chain_ok": scan.HashChainOK,
		"truncated_end": scan.TruncatedEnd,
	}
	if scan.HashError != "" {
		summary["hash_chain_error"] = scan.HashError
	}
	if b, err := os.ReadFile(filepath.Join(dir, "summary.json")); err == nil {
		var s any
		if json.Unmarshal(b, &s) == nil {
			summary["run_summary"] = s
		}
	}
	b, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println(string(b))
	if !scan.HashChainOK {
		return agent.ExitInternal
	}
	return agent.ExitSuccess
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024), 16<<20)
	n := 0
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			n++
		}
	}
	return n, nil
}
