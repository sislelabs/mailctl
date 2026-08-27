package mailsetup

import "testing"

func TestSendingRecordProblem(t *testing.T) {
	cases := []struct {
		name string
		rec  SendingRecord
		ok   bool
		want string
	}{
		{
			name: "healthy",
			rec:  SendingRecord{Present: true, Matches: true, Duplicates: 1, Status: "verified"},
			ok:   true,
		},
		{
			name: "missing",
			rec:  SendingRecord{Status: "pending"},
			want: "missing from DNS",
		},
		{
			// Two DKIM records at one selector is the failure that hid on
			// info.getsaiton.com: the provider still reports verified because
			// it finds its own, while verifiers may pick the other.
			name: "duplicate outranks a verified status",
			rec:  SendingRecord{Present: true, Matches: true, Duplicates: 2, Status: "verified"},
			want: "2 records at this name — verifiers may pick the wrong one",
		},
		{
			name: "content mismatch",
			rec:  SendingRecord{Present: true, Duplicates: 1, Status: "verified"},
			want: "content does not match what the provider expects",
		},
		{
			name: "provider still pending",
			rec:  SendingRecord{Present: true, Matches: true, Duplicates: 1, Status: "pending"},
			want: "provider reports pending",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rec.OK(); got != c.ok {
				t.Errorf("OK() = %v, want %v", got, c.ok)
			}
			if got := c.rec.Problem(); got != c.want {
				t.Errorf("Problem() = %q, want %q", got, c.want)
			}
		})
	}
}
