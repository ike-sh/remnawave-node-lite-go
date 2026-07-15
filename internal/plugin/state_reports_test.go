package plugin

import (
	"fmt"
	"testing"
)

func TestTorrentReportsUseBoundedChronologicalRing(t *testing.T) {
	state := NewState()
	for index := range maxTorrentReports + 2 {
		var report TorrentReport
		report.ActionReport.UserID = fmt.Sprintf("user-%d", index)
		state.AddReport(report)
	}

	if got := state.ReportsCount(); got != maxTorrentReports {
		t.Fatalf("reports count = %d, want %d", got, maxTorrentReports)
	}
	reports, dropped := state.FlushReports()
	if dropped != 2 {
		t.Fatalf("dropped reports = %d, want 2", dropped)
	}
	if got := reports[0].ActionReport.UserID; got != "user-2" {
		t.Fatalf("oldest retained report = %q, want user-2", got)
	}
	if got := reports[len(reports)-1].ActionReport.UserID; got != fmt.Sprintf("user-%d", maxTorrentReports+1) {
		t.Fatalf("newest retained report = %q", got)
	}
	if state.ReportsCount() != 0 {
		t.Fatal("flush did not clear report queue")
	}
}
