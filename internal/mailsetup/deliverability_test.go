package mailsetup

import (
	"strings"
	"testing"
	"time"
)

func TestStageFor(t *testing.T) {
	cases := []struct {
		name string
		d    Deliverability
		want Stage
	}{
		{"never sent", Deliverability{Sent: 0}, StageNoHistory},
		{"a couple of sends today", Deliverability{Sent: 2, Age: time.Hour}, StageWarming},
		{"decent volume but young", Deliverability{Sent: 200, Age: 2 * 24 * time.Hour}, StageWarming},
		{"long-lived but low volume", Deliverability{Sent: 10, Age: 90 * 24 * time.Hour}, StageWarming},
		{"volume and time", Deliverability{Sent: 200, Age: 90 * 24 * time.Hour}, StageEstablished},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stageFor(&c.d); got != c.want {
				t.Errorf("stageFor = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAdviceLeadsWithTheRealProblem(t *testing.T) {
	// A domain with perfect DNS and no history is the case that looks fine and
	// lands in spam, so its first line of advice has to say why.
	cold := &Deliverability{Sent: 0, DMARCPolicy: "none", DMARCReports: true}
	advice := cold.Advice()
	if len(advice) == 0 {
		t.Fatal("a domain that has never sent should get advice")
	}
	if !strings.Contains(advice[0], "never sent") {
		t.Errorf("first advice should name the cause, got %q", advice[0])
	}

	// An established domain with clean numbers needs nothing said.
	good := &Deliverability{
		Sent: 500, Delivered: 500, Age: 200 * 24 * time.Hour,
		DMARCPolicy: "quarantine", DMARCReports: true,
	}
	if got := good.Advice(); len(got) != 0 {
		t.Errorf("a healthy domain should get no advice, got %v", got)
	}
}

func TestAdviceFlagsBouncesAndComplaints(t *testing.T) {
	d := &Deliverability{
		Sent: 100, Bounced: 5, Complained: 2, Age: 200 * 24 * time.Hour,
		DMARCPolicy: "none", DMARCReports: true,
	}
	joined := strings.Join(d.Advice(), " ")
	if !strings.Contains(joined, "Bounce rate") {
		t.Error("a 5% bounce rate should be called out")
	}
	if !strings.Contains(joined, "marked mail as spam") {
		t.Error("complaints should be called out")
	}

	// A couple of bounces in a tiny sample is noise, not a signal.
	small := &Deliverability{Sent: 10, Bounced: 1, Age: 200 * 24 * time.Hour, DMARCPolicy: "none", DMARCReports: true}
	if strings.Contains(strings.Join(small.Advice(), " "), "Bounce rate") {
		t.Error("a small sample should not trigger a bounce warning")
	}
}

func TestBounceRateWithNoSends(t *testing.T) {
	d := &Deliverability{}
	if d.BounceRate() != 0 {
		t.Error("no sends must not divide by zero")
	}
}
