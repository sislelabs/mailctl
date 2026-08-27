package dmarc

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

const sample = `<?xml version="1.0" encoding="UTF-8" ?>
<feedback>
  <report_metadata>
    <org_name>google.com</org_name>
    <date_range><begin>1755907200</begin><end>1755993600</end></date_range>
  </report_metadata>
  <policy_published>
    <domain>example.com</domain><adkim>r</adkim><aspf>r</aspf><p>none</p><sp>none</sp><pct>100</pct>
  </policy_published>
  <record>
    <row><source_ip>1.2.3.4</source_ip><count>7</count>
      <policy_evaluated><disposition>none</disposition><dkim>pass</dkim><spf>pass</spf></policy_evaluated></row>
    <identifiers><header_from>example.com</header_from></identifiers>
  </record>
  <record>
    <row><source_ip>5.6.7.8</source_ip><count>2</count>
      <policy_evaluated><disposition>none</disposition><dkim>fail</dkim><spf>fail</spf></policy_evaluated></row>
    <identifiers><header_from>example.com</header_from></identifiers>
  </record>
</feedback>`

func TestParsePlain(t *testing.T) {
	r, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Policy.Domain != "example.com" || r.Metadata.OrgName != "google.com" {
		t.Errorf("metadata wrong: %+v", r.Policy)
	}
	total, passed, failed := r.Totals()
	if total != 9 || passed != 7 || failed != 2 {
		t.Errorf("totals = %d/%d/%d, want 9/7/2", total, passed, failed)
	}
	if got := r.PercentPassed(); got < 77 || got > 78 {
		t.Errorf("PercentPassed = %v", got)
	}
}

func TestParseHandlesGzipAndZip(t *testing.T) {
	// Google sends zip, most other receivers send gzip, and a file saved out
	// of a mail client may already be plain XML.
	var gzBuf bytes.Buffer
	zw := gzip.NewWriter(&gzBuf)
	zw.Write([]byte(sample))
	zw.Close()

	var zipBuf bytes.Buffer
	zwr := zip.NewWriter(&zipBuf)
	f, _ := zwr.Create("report.xml")
	f.Write([]byte(sample))
	zwr.Close()

	for name, data := range map[string][]byte{
		"gzip":  gzBuf.Bytes(),
		"zip":   zipBuf.Bytes(),
		"plain": []byte(sample),
	} {
		r, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total, _, _ := r.Totals(); total != 9 {
			t.Errorf("%s: total = %d, want 9", name, total)
		}
	}
}

func TestPassedUsesTheAlignedVerdict(t *testing.T) {
	// DMARC passes when a mechanism passes *and* aligns. policy_evaluated
	// records that; raw auth_results can show a pass that did not align and
	// therefore did not count.
	var rec Record
	rec.Row.PolicyEvaluated.DKIM = "pass"
	rec.Row.PolicyEvaluated.SPF = "fail"
	if !rec.Passed() {
		t.Error("one aligned pass is enough for DMARC")
	}

	rec.Row.PolicyEvaluated.DKIM = "fail"
	if rec.Passed() {
		t.Error("neither mechanism aligned, so DMARC failed")
	}
}

func TestParseRejectsUnrelatedXML(t *testing.T) {
	if _, err := Parse([]byte(`<html><body>not a report</body></html>`)); err == nil {
		t.Error("expected an error for a non-report file")
	}
}

func TestPolicySummary(t *testing.T) {
	r, _ := Parse([]byte(sample))
	if got := r.PolicySummary(); got != "p=none (adkim=r aspf=r)" {
		t.Errorf("PolicySummary = %q", got)
	}
}
