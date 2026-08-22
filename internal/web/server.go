// Package web serves mailctl's local control panel.
//
// The server is deliberately single-user and localhost-only: it reads the same
// config the CLI does and holds the same Cloudflare and Resend credentials.
// Those credentials carry DNS edit rights across every zone the token can see,
// so exposing this on a network without an authentication and authorisation
// model in front of it would be a serious escalation over a file in a home
// directory. Binding elsewhere requires an explicit opt-in.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/mailsetup"
	"github.com/sislelabs/mailctl/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server serves the panel from a Store.
type Server struct {
	store store.Store
	// pages are full documents, one template set each. Go collapses same-named
	// define blocks across a single set, and every page defines "content", so
	// they cannot share one.
	pages map[string]*template.Template
	// frags are the partials htmx swaps in.
	frags *template.Template
}

// NewServer builds a server backed by st.
func NewServer(st store.Store) (*Server, error) {
	funcs := template.FuncMap{
		"levelClass": levelClass,
		"join":       strings.Join,
	}

	srv := &Server{store: st, pages: map[string]*template.Template{}}

	// Each page gets the layout and every shared partial.
	for _, page := range []string{"overview.html", "domain.html"} {
		t, err := template.New(page).Funcs(funcs).ParseFS(templateFS,
			"templates/layout.html", "templates/aliases.html", "templates/audit.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		srv.pages[page] = t
	}

	frags, err := template.New("frags").Funcs(funcs).ParseFS(templateFS,
		"templates/aliases.html", "templates/audit.html")
	if err != nil {
		return nil, fmt.Errorf("parse fragments: %w", err)
	}
	srv.frags = frags

	return srv, nil
}

// Handler returns the panel's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", s.handleOverview)
	mux.HandleFunc("GET /audit", s.handleAudit)
	mux.HandleFunc("GET /domains/{domain}", s.handleDomain)
	mux.HandleFunc("POST /domains/{domain}/aliases", s.handleAddAlias)
	mux.HandleFunc("DELETE /domains/{domain}/aliases/{alias}", s.handleRemoveAlias)
	return mux
}

func levelClass(l mailsetup.FindingLevel) string {
	switch l {
	case mailsetup.FindingProblem:
		return "problem"
	case mailsetup.FindingWarn:
		return "warn"
	default:
		return "info"
	}
}

// render writes a full page.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderFragment writes a partial for htmx to swap in.
func (s *Server) renderFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.frags.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type overviewData struct {
	Title   string
	Config  *internal.Config
	Domains []domainRow
}

type domainRow struct {
	Domain    string
	Aliases   []internal.Alias
	ForwardTo string
	Tracked   int
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := overviewData{Title: "Domains", Config: cfg}
	for i := range cfg.Domains {
		d := &cfg.Domains[i]
		row := domainRow{
			Domain:  d.Domain,
			Aliases: d.Aliases,
			Tracked: len(d.ManagedDNSRecordIDs),
		}
		if len(d.Aliases) > 0 && len(d.Aliases[0].ForwardTo) > 0 {
			row.ForwardTo = d.Aliases[0].ForwardTo[0]
		} else {
			row.ForwardTo = cfg.DefaultForwardTo
		}
		data.Domains = append(data.Domains, row)
	}

	s.render(w, "overview.html", data)
}

type auditData struct {
	Report   *mailsetup.AuditReport
	Problems int
	Warnings int
	Infos    int
}

// handleAudit runs the reconciliation. It is slow — several API round trips —
// so the overview loads it lazily over htmx rather than blocking the page.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	report := mailsetup.Audit(cfg)
	problems, warnings, infos := report.Counts()
	s.renderFragment(w, "audit", auditData{
		Report:   report,
		Problems: problems,
		Warnings: warnings,
		Infos:    infos,
	})
}

type domainData struct {
	Title     string
	Domain    *internal.DomainConfig
	ForwardTo string
	Error     string
}

func (s *Server) handleDomain(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d := cfg.FindDomain(r.PathValue("domain"))
	if d == nil {
		http.NotFound(w, r)
		return
	}

	s.render(w, "domain.html", domainData{Title: d.Domain, Domain: d, ForwardTo: cfg.DefaultForwardTo})
}

// handleAddAlias creates an alias and re-renders just the alias list, which is
// the fragment htmx swaps in.
func (s *Server) handleAddAlias(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	alias := strings.TrimSpace(r.FormValue("alias"))

	addErr := ""
	if err := mailsetup.AddAlias(s.store, domain, alias, r.FormValue("forward_to")); err != nil {
		addErr = err.Error()
	}
	s.renderAliases(w, domain, addErr)
}

func (s *Server) handleRemoveAlias(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")

	removeErr := ""
	if err := mailsetup.RemoveAlias(s.store, domain, r.PathValue("alias")); err != nil {
		removeErr = err.Error()
	}
	s.renderAliases(w, domain, removeErr)
}

func (s *Server) renderAliases(w http.ResponseWriter, domain, errMsg string) {
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		http.NotFound(w, nil)
		return
	}
	s.renderFragment(w, "aliases", domainData{Domain: d, ForwardTo: cfg.DefaultForwardTo, Error: errMsg})
}
