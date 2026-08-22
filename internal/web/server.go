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
	jobs  *jobs
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
		"stepClass":  stepClass,
		"noteClass":  noteClass,
	}

	srv := &Server{store: st, jobs: newJobs(), pages: map[string]*template.Template{}}

	// Each page gets the layout and every shared partial.
	for _, page := range []string{"overview.html", "domain.html"} {
		t, err := template.New(page).Funcs(funcs).ParseFS(templateFS,
			"templates/layout.html", "templates/aliases.html", "templates/audit.html",
			"templates/job.html", "templates/addform.html", "templates/removeconfirm.html",
			"templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		srv.pages[page] = t
	}

	frags, err := template.New("frags").Funcs(funcs).ParseFS(templateFS,
		"templates/aliases.html", "templates/audit.html", "templates/job.html",
		"templates/addform.html", "templates/removeconfirm.html")
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
	mux.HandleFunc("GET /domains/{domain}/aliases", s.handleAliases)
	mux.HandleFunc("POST /domains/{domain}/aliases", s.handleAddAlias)
	mux.HandleFunc("DELETE /domains/{domain}/aliases/{alias}", s.handleRemoveAlias)
	mux.HandleFunc("POST /domains/{domain}/aliases/{alias}/forward", s.handleRepointAlias)
	mux.HandleFunc("POST /domains", s.handleAddDomain)
	mux.HandleFunc("GET /domains/{domain}/remove", s.handleRemoveConfirm)
	mux.HandleFunc("DELETE /domains/{domain}", s.handleRemoveDomain)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
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
	// AddForm is passed to the shared add-domain fragment, which the same
	// handler re-renders on validation failure.
	AddForm addFormData
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

	data := overviewData{Title: "Domains", Config: cfg, AddForm: addFormData{Config: cfg}}
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
	Notice    string
	// Aliases is read live from Cloudflare rather than from config, which is
	// only a cache and goes stale as soon as a rule is edited elsewhere.
	Aliases      []mailsetup.AliasView
	AliasesError string
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

	// The alias list needs a live Cloudflare read, so the page ships without it
	// and htmx fills it in. Blocking here would make the panel feel broken on a
	// slow link and unusable without one.
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

// handleAliases renders the live alias list htmx pulls in.
func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	s.renderAliases(w, r.PathValue("domain"), "")
}

// handleRepointAlias changes where one address forwards.
func (s *Server) handleRepointAlias(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")

	notice, errMsg := "", ""
	result, err := mailsetup.RepointAlias(s.store, domain, r.PathValue("alias"), r.FormValue("forward_to"))
	switch {
	case err != nil:
		errMsg = err.Error()
	case result.DestinationCreated:
		notice = "Confirmation email sent to " + r.FormValue("forward_to") +
			" — Cloudflare will not deliver there until it is confirmed."
	case !result.DestinationVerified:
		notice = r.FormValue("forward_to") + " is registered but not yet confirmed — mail will not be delivered until it is."
	}
	s.renderAliasesWith(w, domain, errMsg, notice)
}

func (s *Server) renderAliases(w http.ResponseWriter, domain, errMsg string) {
	s.renderAliasesWith(w, domain, errMsg, "")
}

func (s *Server) renderAliasesWith(w http.ResponseWriter, domain, errMsg, notice string) {
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		http.NotFound(w, &http.Request{})
		return
	}
	data := domainData{Domain: d, ForwardTo: cfg.DefaultForwardTo, Error: errMsg, Notice: notice}
	if views, err := mailsetup.ListAliases(cfg, d); err != nil {
		data.AliasesError = err.Error()
	} else {
		data.Aliases = views
	}
	s.renderFragment(w, "aliases", data)
}

type jobData struct {
	Job     *Job
	Steps   []JobStep
	Done    bool
	Error   string
	Success bool
}

// handleAddDomain starts a domain setup and hands back the progress view.
func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	opts := mailsetup.AddOptions{
		Domain:    strings.TrimSpace(r.FormValue("domain")),
		Aliases:   strings.Split(r.FormValue("aliases"), ","),
		ForwardTo: strings.TrimSpace(r.FormValue("forward_to")),
	}

	// Reject bad input before starting a job, so a typo comes back as a
	// message instead of a progress view that fails on its first step.
	if err := mailsetup.ValidateAdd(cfg, opts); err != nil {
		s.renderFragment(w, "addform", addFormData{Config: cfg, Error: err.Error()})
		return
	}

	job := newJob("add", opts.Domain, mailsetup.AddStepLabels(mailsetup.ProviderLabel(cfg)))
	if !s.jobs.start(job, func() error {
		_, err := mailsetup.AddDomain(s.store, job, opts)
		return err
	}) {
		s.renderFragment(w, "addform", addFormData{Config: cfg, Error: "another domain operation is already running"})
		return
	}

	s.renderJob(w, job)
}

// handleRemoveConfirm renders what teardown would delete, mirroring the CLI's
// typed confirmation rather than a one-click destructive button.
func (s *Server) handleRemoveConfirm(w http.ResponseWriter, r *http.Request) {
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

	s.renderFragment(w, "removeconfirm", removeConfirmData{
		Domain:   d,
		Provider: mailsetup.ProviderLabel(cfg),
		Tracked:  len(d.ManagedDNSRecordIDs),
	})
}

// handleRemoveDomain starts a teardown.
func (s *Server) handleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")

	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := cfg.FindDomain(domain)
	if d == nil {
		http.NotFound(w, r)
		return
	}

	// The typed name has to match, the same guard the CLI uses.
	if strings.TrimSpace(r.FormValue("confirm")) != domain {
		s.renderFragment(w, "removeconfirm", removeConfirmData{
			Domain:   d,
			Provider: mailsetup.ProviderLabel(cfg),
			Tracked:  len(d.ManagedDNSRecordIDs),
			Error:    "Type the domain name exactly to confirm.",
		})
		return
	}

	job := newJob("remove", domain, mailsetup.RemoveStepLabels(d, mailsetup.ProviderLabel(cfg)))
	if !s.jobs.start(job, func() error {
		_, err := mailsetup.RemoveDomain(s.store, job, mailsetup.RemoveOptions{Domain: domain})
		return err
	}) {
		s.renderFragment(w, "removeconfirm", removeConfirmData{
			Domain:   d,
			Provider: mailsetup.ProviderLabel(cfg),
			Tracked:  len(d.ManagedDNSRecordIDs),
			Error:    "Another domain operation is already running.",
		})
		return
	}

	s.renderJob(w, job)
}

// handleJob renders a job's current state. The fragment polls itself until the
// work finishes and then stops.
func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	job := s.jobs.get(r.PathValue("id"))
	if job == nil {
		http.NotFound(w, r)
		return
	}
	s.renderJob(w, job)
}

func (s *Server) renderJob(w http.ResponseWriter, job *Job) {
	steps, done, errMsg := job.Snapshot()
	s.renderFragment(w, "job", jobData{
		Job:     job,
		Steps:   steps,
		Done:    done,
		Error:   errMsg,
		Success: done && errMsg == "",
	})
}

type addFormData struct {
	Config *internal.Config
	Error  string
}

type removeConfirmData struct {
	Domain   *internal.DomainConfig
	Provider string
	Tracked  int
	Error    string
}

func stepClass(s mailsetup.StepStatus) string {
	switch s {
	case mailsetup.StepRunning:
		return "running"
	case mailsetup.StepDone:
		return "done"
	case mailsetup.StepWarn:
		return "warn"
	case mailsetup.StepFailed:
		return "problem"
	default:
		return "pending"
	}
}

func noteClass(l mailsetup.NoteLevel) string {
	switch l {
	case mailsetup.NoteWarn:
		return "warn"
	case mailsetup.NoteError:
		return "problem"
	default:
		return "info"
	}
}
