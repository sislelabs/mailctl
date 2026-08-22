package mailsetup

import "testing"

func TestSyncChangeKindString(t *testing.T) {
	cases := map[SyncChangeKind]string{
		SyncRepointed: "repointed",
		SyncAdopted:   "adopted",
		SyncDropped:   "dropped",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}

func TestSyncNoMatchingDomain(t *testing.T) {
	// Naming a domain that is not configured is a mistake worth reporting
	// rather than silently doing nothing.
	_, err := Sync(&stubStore{}, []string{"nope.com"}, true)
	if err == nil {
		t.Error("expected an error for an unknown domain")
	}
}
