package mailsetup

import (
	"fmt"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/store"
)

// SyncChangeKind classifies one config adjustment.
type SyncChangeKind int

const (
	// SyncRepointed means config recorded a different destination than the one
	// mail actually routes to.
	SyncRepointed SyncChangeKind = iota
	// SyncAdopted means Cloudflare routes an address config knew nothing about.
	SyncAdopted
	// SyncDropped means config listed an address with no routing rule behind it.
	SyncDropped
)

func (k SyncChangeKind) String() string {
	switch k {
	case SyncRepointed:
		return "repointed"
	case SyncAdopted:
		return "adopted"
	default:
		return "dropped"
	}
}

// SyncChange is one difference reconciled on a domain.
type SyncChange struct {
	Kind    SyncChangeKind
	Alias   string
	Address string
	// Was is what config held; Now is what Cloudflare routes.
	Was []string
	Now []string
}

// SyncResult reports the reconciliation for one domain.
type SyncResult struct {
	Domain  string
	Changes []SyncChange
	// Err is set when the domain could not be read; its changes are empty.
	Err error
}

// Sync reconciles config with live Cloudflare routing.
//
// It only ever writes to config. Cloudflare is the source of truth for where
// mail goes — rules can be edited in its dashboard, and mailctl's copy goes
// stale the moment they are — so adopting the live state is the only safe
// direction. Nothing in Cloudflare is created, changed or deleted.
//
// domains limits the run; empty means every configured domain. When dryRun is
// set the changes are reported without being saved.
func Sync(st store.Store, domains []string, dryRun bool) ([]SyncResult, error) {
	cfg, err := st.Load()
	if err != nil {
		return nil, err
	}

	wanted := map[string]bool{}
	for _, d := range domains {
		wanted[strings.ToLower(strings.TrimSpace(d))] = true
	}

	var results []SyncResult
	changed := false

	for i := range cfg.Domains {
		d := &cfg.Domains[i]
		if len(wanted) > 0 && !wanted[strings.ToLower(d.Domain)] {
			continue
		}

		result := syncDomain(cfg, d)
		results = append(results, result)
		if len(result.Changes) > 0 {
			changed = true
		}
	}

	if len(wanted) > 0 && len(results) == 0 {
		return nil, fmt.Errorf("no configured domain matched")
	}

	if changed && !dryRun {
		if err := st.Save(cfg); err != nil {
			return results, err
		}
	}
	return results, nil
}

// syncDomain reconciles one domain, mutating its config entry in place.
func syncDomain(cfg *internal.Config, d *internal.DomainConfig) SyncResult {
	result := SyncResult{Domain: d.Domain}

	views, err := ListAliases(cfg, d)
	if err != nil {
		result.Err = err
		return result
	}

	live := map[string]bool{}
	for _, v := range views {
		live[v.Alias] = true

		switch {
		case v.Untracked:
			result.Changes = append(result.Changes, SyncChange{
				Kind: SyncAdopted, Alias: v.Alias, Address: v.Address, Now: v.ForwardTo,
			})
			d.AddAlias(v.Alias, v.ForwardTo)
		case v.Drifted:
			result.Changes = append(result.Changes, SyncChange{
				Kind: SyncRepointed, Alias: v.Alias, Address: v.Address,
				Was: v.ConfigSays, Now: v.ForwardTo,
			})
			if a := d.FindAlias(v.Alias); a != nil {
				a.ForwardTo = v.ForwardTo
			}
		}
	}

	// Aliases config still lists with no rule behind them. Dropping them from
	// config removes a record of something that is not routing; it deletes
	// nothing in Cloudflare.
	for _, a := range append([]internal.Alias(nil), d.Aliases...) {
		if !live[a.Alias] {
			result.Changes = append(result.Changes, SyncChange{
				Kind: SyncDropped, Alias: a.Alias,
				Address: a.Alias + "@" + d.Domain, Was: a.ForwardTo,
			})
			d.RemoveAlias(a.Alias)
		}
	}

	return result
}
