// Package dmarc reads DMARC aggregate reports.
//
// Receivers send these daily as gzipped or zipped XML, which is the only
// feedback anyone gets on whether their mail authenticates in the wild. The
// format is unreadable by hand, so in practice the reports are either pasted
// into a third-party analyser — handing over sending IPs, volumes and
// infrastructure layout — or ignored. Reading them locally avoids both.
package dmarc

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Report is one aggregate report from one receiver.
type Report struct {
	XMLName  xml.Name `xml:"feedback"`
	Metadata struct {
		OrgName   string `xml:"org_name"`
		Email     string `xml:"email"`
		ReportID  string `xml:"report_id"`
		DateRange struct {
			Begin int64 `xml:"begin"`
			End   int64 `xml:"end"`
		} `xml:"date_range"`
	} `xml:"report_metadata"`

	Policy struct {
		Domain string `xml:"domain"`
		ADKIM  string `xml:"adkim"`
		ASPF   string `xml:"aspf"`
		P      string `xml:"p"`
		SP     string `xml:"sp"`
		Pct    string `xml:"pct"`
	} `xml:"policy_published"`

	Records []Record `xml:"record"`
}

// Record is one sending source's results for the reporting period.
type Record struct {
	Row struct {
		SourceIP        string `xml:"source_ip"`
		Count           int    `xml:"count"`
		PolicyEvaluated struct {
			Disposition string `xml:"disposition"`
			DKIM        string `xml:"dkim"`
			SPF         string `xml:"spf"`
		} `xml:"policy_evaluated"`
	} `xml:"row"`

	Identifiers struct {
		HeaderFrom string `xml:"header_from"`
	} `xml:"identifiers"`

	AuthResults struct {
		DKIM []struct {
			Domain   string `xml:"domain"`
			Selector string `xml:"selector"`
			Result   string `xml:"result"`
		} `xml:"dkim"`
		SPF []struct {
			Domain string `xml:"domain"`
			Result string `xml:"result"`
		} `xml:"spf"`
	} `xml:"auth_results"`
}

// Begin and End bound the reporting period.
func (r *Report) Begin() time.Time { return time.Unix(r.Metadata.DateRange.Begin, 0) }
func (r *Report) End() time.Time   { return time.Unix(r.Metadata.DateRange.End, 0) }

// Passed reports whether a record passed DMARC. DMARC passes when either
// mechanism passes *and* aligns, which is what policy_evaluated records — the
// raw auth_results can show a pass that did not align and so did not count.
func (rec *Record) Passed() bool {
	return rec.Row.PolicyEvaluated.DKIM == "pass" || rec.Row.PolicyEvaluated.SPF == "pass"
}

// Totals counts messages across every record.
func (r *Report) Totals() (total, passed, failed int) {
	for i := range r.Records {
		n := r.Records[i].Row.Count
		total += n
		if r.Records[i].Passed() {
			passed += n
		} else {
			failed += n
		}
	}
	return
}

// PolicySummary renders the published policy compactly.
func (r *Report) PolicySummary() string {
	out := "p=" + r.Policy.P
	if r.Policy.SP != "" && r.Policy.SP != r.Policy.P {
		out += " sp=" + r.Policy.SP
	}
	if pct := r.Policy.Pct; pct != "" && pct != "100" {
		out += " pct=" + pct
	}
	if r.Policy.ADKIM != "" || r.Policy.ASPF != "" {
		out += fmt.Sprintf(" (adkim=%s aspf=%s)", orDefault(r.Policy.ADKIM, "r"), orDefault(r.Policy.ASPF, "r"))
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Parse reads a report from raw bytes, transparently handling the gzip and zip
// wrappers receivers use. Google sends zip, most others send gzip, and a file
// saved out of a mail client may already be plain XML.
func Parse(data []byte) (*Report, error) {
	xmlData, err := unwrap(data)
	if err != nil {
		return nil, err
	}

	var report Report
	if err := xml.Unmarshal(xmlData, &report); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	if report.Policy.Domain == "" && len(report.Records) == 0 {
		return nil, fmt.Errorf("this does not look like a DMARC aggregate report")
	}
	return &report, nil
}

func unwrap(data []byte) ([]byte, error) {
	switch {
	case len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b:
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("read gzip: %w", err)
		}
		defer zr.Close()
		return io.ReadAll(zr)

	case len(data) > 4 && data[0] == 'P' && data[1] == 'K':
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("read zip: %w", err)
		}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			out, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			return out, nil
		}
		return nil, fmt.Errorf("zip archive is empty")

	default:
		return data, nil
	}
}

// PercentPassed is the share of messages that passed DMARC, 0 when none were
// reported.
func (r *Report) PercentPassed() float64 {
	total, passed, _ := r.Totals()
	if total == 0 {
		return 0
	}
	return float64(passed) / float64(total) * 100
}

// CountString renders a count for display.
func CountString(n int) string { return strconv.Itoa(n) }
