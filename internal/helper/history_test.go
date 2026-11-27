package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

// applyRecord is a run's record of one apply that updated history.yaml and restarted history.
func applyRecord(applyID string) wire.History {
	return wire.History{
		ApplyID: applyID, RunID: "r-1", Command: "apply", By: "ssk@thinkpad", TopsVersion: "v0.3.0",
		Env: "lab", Cluster: "tops-e2e", Node: "vps-01", SpecSHA256: sha("spec"),
		StartedAt: t0, FinishedAt: t0.Add(time.Minute),
		Changes:   []wire.Change{{Path: historyYAML.Path, Role: "history", Action: wire.ChangeUpdate, Effect: "restart", BeforeSHA256: sha("a"), AfterSHA256: sha("b")}},
		Restarted: []string{"history"}, Started: []string{}, Stopped: []string{},
		Result: wire.ResultOK,
	}
}

// listed is the node's newest limit records.
func listed(t *testing.T, h Host, limit int) []wire.History {
	t.Helper()
	res, err := h.ListHistory(limit)
	must(t, err)
	return res.Records
}

// TestHistoryAppend: a record is kept root's alone and listed as it was appended; it says
// whether its apply's snapshot is still there; a run's second append replaces its first.
func TestHistoryAppend(t *testing.T) {
	h := newWriteNode(t)
	if got := listed(t, h, 20); got == nil || len(got) != 0 {
		t.Errorf("a node without history lists %#v, want none", got)
	}
	must(t, h.AppendHistory(applyRecord(applyA)))
	if got := listed(t, h, 20); !reflect.DeepEqual(got, []wire.History{applyRecord(applyA)}) {
		t.Errorf("listed:\n got %+v\nwant %+v", got, applyRecord(applyA))
	}
	for path, want := range map[string]os.FileMode{historyDir: os.ModeDir | 0o700, historyDir + "/" + applyA + ".json": 0o600} {
		if info, err := os.Stat(filepath.Join(h.Root, path)); err != nil || info.Mode() != want {
			t.Errorf("%s: %v, %v; want %v", path, info.Mode(), err, want)
		}
	}
	mustWrite(t, h, applyB, wire.Write{Files: []wire.File{historyYAML}})
	failed := applyRecord(applyB)
	failed.Result, failed.Error = wire.ResultFailed, "gate after: history ring 1/2"
	must(t, h.AppendHistory(failed))
	retried := applyRecord(applyB)
	must(t, h.AppendHistory(retried))
	retried.Snapshot = true
	if got := listed(t, h, 20); !reflect.DeepEqual(got, []wire.History{retried, applyRecord(applyA)}) {
		t.Errorf("after a retry:\n got %+v", got)
	}
}

// TestHistoryPruned: the newest hundred records are kept, the run's own even when a slow
// workstation clock makes it the oldest; list returns the newest first, twenty by default.
func TestHistoryPruned(t *testing.T) {
	h := newWriteNode(t)
	id := func(i int) string { return fmt.Sprintf("20261124T%02d%02d00Z-aaaa", i/60, i%60) }
	for i := range 102 {
		must(t, h.AppendHistory(applyRecord(id(i))))
	}
	if got := listed(t, h, 100); len(got) != 100 || got[0].ApplyID != id(101) || got[99].ApplyID != id(2) {
		t.Fatalf("kept %d records, want the newest hundred", len(got))
	}
	if got := listed(t, h, 20); len(got) != 20 || got[19].ApplyID != id(82) {
		t.Errorf("listed %d", len(got))
	}
	const slow = "20200101T000000Z-aaaa"
	must(t, h.AppendHistory(applyRecord(slow)))
	if got := listed(t, h, 100); len(got) != 100 || got[99].ApplyID != slow || got[0].ApplyID != id(101) {
		t.Errorf("a slow clock's append kept %d records, want its own among a hundred", len(got))
	}
}

// TestHistoryCorrupt: a record the helper cannot read fails the list; a temporary file a
// killed append left is not a record.
func TestHistoryCorrupt(t *testing.T) {
	h := newWriteNode(t)
	must(t, h.AppendHistory(applyRecord(applyA)))
	writeFile(t, filepath.Join(h.Root, historyDir, "."+applyB+".json"+tempMark+"123"), "{")
	if got := listed(t, h, 20); len(got) != 1 {
		t.Errorf("listed %d records, want the one", len(got))
	}
	writeFile(t, filepath.Join(h.Root, historyDir, applyB+".json"), `{"applyId":"`+applyB+`","content":"s3cret"}`)
	if _, err := h.ListHistory(20); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("list = %v, want the record corrupt", err)
	}
}

// historyRequest is a history request of the run r-1, under applyID.
func historyRequest(t *testing.T, applyID, args string) string {
	t.Helper()
	b, err := json.Marshal(wire.Request{RunID: "r-1", ApplyID: applyID, By: "ssk@thinkpad", Args: json.RawMessage(args)})
	must(t, err)
	return string(b)
}

// appendArgs is an append of r, its changes replaced by change when that is not "".
func appendArgs(t *testing.T, r wire.History, change string) string {
	t.Helper()
	b, err := json.Marshal(wire.HistoryArgs{Action: wire.HistoryAppend, Record: &r})
	must(t, err)
	if change == "" {
		return string(b)
	}
	var raw struct {
		Action string         `json:"action"`
		Record map[string]any `json:"record"`
	}
	must(t, json.Unmarshal(b, &raw))
	raw.Record["changes"] = json.RawMessage("[" + change + "]")
	b, err = json.Marshal(raw)
	must(t, err)
	return string(b)
}

// TestRunHistoryRefused: only append and list; a list beyond the hundred kept; an append
// without an apply id, without a result, or with a change that carries anything but a path
// and its hashes, content above all. Nothing refused is kept.
func TestRunHistoryRefused(t *testing.T) {
	h := newWriteNode(t)
	leased(t, &h, "r-1")
	failed := applyRecord(applyA)
	failed.Result = "done"
	refused := map[string]string{
		"no action":          `{}`,
		"unknown action":     `{"action":"remove"}`,
		"list too long":      `{"action":"list","limit":101}`,
		"list negative":      `{"action":"list","limit":-1}`,
		"list with a record": `{"action":"list","record":{}}`,
		"append no record":   `{"action":"append"}`,
		"append with limit":  `{"action":"append","limit":5,"record":` + appendArgs(t, applyRecord(applyA), "")[len(`{"action":"append","record":`):],
		"unknown result":     appendArgs(t, failed, ""),
		"content":            appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/temporal.env","action":"update","beforeSha256":"`+sha("a")+`","afterSha256":"`+sha("b")+`","content":"czNjcmV0"}`),
		"secret in a hash":   appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/temporal.env","action":"create","afterSha256":"TEMPORAL_PG_PASSWORD=s3cret"}`),
		"create with before": appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/x","action":"create","beforeSha256":"`+sha("a")+`","afterSha256":"`+sha("b")+`"}`),
		"remove with after":  appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/x","action":"remove","beforeSha256":"`+sha("a")+`","afterSha256":"`+sha("b")+`"}`),
		"update one hash":    appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/x","action":"update","afterSha256":"`+sha("b")+`"}`),
		"unknown change":     appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/x","action":"chmod","beforeSha256":"`+sha("a")+`","afterSha256":"`+sha("a")+`"}`),
		"relative path":      appendArgs(t, applyRecord(applyA), `{"path":"etc/temporal/x","action":"create","afterSha256":"`+sha("b")+`"}`),
		"unclean path":       appendArgs(t, applyRecord(applyA), `{"path":"/etc/temporal/../x","action":"create","afterSha256":"`+sha("b")+`"}`),
	}
	for name, args := range refused {
		if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpHistory, historyRequest(t, applyA, args)); code != ExitBadRequest || resp.Error.Code != codeBadRequest {
			t.Errorf("%s: exit %d, %+v, want a bad request", name, code, resp.Error)
		}
	}
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpHistory, historyRequest(t, "", appendArgs(t, applyRecord(applyA), ""))); code != ExitBadRequest || resp.Error.Code != codeBadRequest {
		t.Errorf("without an apply id: exit %d, %+v, want a bad request", code, resp.Error)
	}
	if got := listed(t, h, 100); len(got) != 0 {
		t.Errorf("refused appends kept %+v", got)
	}
}

// TestRunHistoryAppend: append is the lease holder's alone, and names the record by the
// request's run and apply id, whatever the record says.
func TestRunHistoryAppend(t *testing.T) {
	h := newWriteNode(t)
	h.Now = time.Now
	other := applyRecord(applyB)
	other.RunID = "r-other"
	appendReq := historyRequest(t, applyA, appendArgs(t, other, ""))
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpHistory, appendReq); code != ExitNotOK || resp.Error.Code != codeLockLost {
		t.Errorf("without the lease: exit %d, %+v", code, resp.Error)
	}
	leased(t, &h, "r-1")
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpHistory, appendReq); code != ExitOK || string(resp.Result) != "{}" {
		t.Fatalf("append: exit %d, %+v, %s", code, resp.Error, resp.Result)
	}
	if got := listed(t, h, 20); len(got) != 1 || got[0].RunID != "r-1" || got[0].ApplyID != applyA {
		t.Errorf("kept %+v", got)
	}
}

// listRequest lists through the op, as a run that holds no lease.
func listRequest(t *testing.T, h Host, args string) []wire.History {
	t.Helper()
	code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpHistory, historyRequest(t, "", args))
	var res wire.HistoryResult
	if code != ExitOK || json.Unmarshal(resp.Result, &res) != nil {
		t.Fatalf("%s: exit %d, %+v, %s", args, code, resp.Error, resp.Result)
	}
	return res.Records
}

// TestRunHistoryList: list needs no lease, and no apply id; 0 is twenty.
func TestRunHistoryList(t *testing.T) {
	h := newWriteNode(t)
	must(t, h.AppendHistory(applyRecord(applyA)))
	must(t, h.AppendHistory(applyRecord(applyB)))
	if got := listRequest(t, h, `{"action":"list","limit":1}`); len(got) != 1 || got[0].ApplyID != applyB {
		t.Errorf("list 1: %+v", got)
	}
	if got := listRequest(t, h, `{"action":"list"}`); len(got) != 2 {
		t.Errorf("list: %+v", got)
	}
}
