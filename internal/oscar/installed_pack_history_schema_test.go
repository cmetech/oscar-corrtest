package oscar

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"
)

// Fixture generated with actual middleware AlertHistoryListResponse.model_dump_json;
// schema SHA256 20c6e3cb4a81db231bc3572b0dbc144bc71a5058f1b4f7d85eba7716dec86247.
// Synthetic valid record, actual serializer; not service/SQL evidence.
func TestInstalledHistoryActualPublicSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/installed-history-page.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/ext/mw"} {
		t.Run(prefix, func(t *testing.T) {
			_, target, _ := installedPublicProxy(t, prefix, "key", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
			options := Options{Getenv: func(string) string { return "key" }}
			if prefix == "" {
				legacy, err := NewExistingFeedHistoryReader(target, options)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := legacy.FindHistory(context.Background(), HistoryQuery{AlertName: "CorrTestOperationalFlood"})
				if err != nil || len(rows) != 1 {
					t.Fatalf("legacy actual schema failed %v", err)
				}
			}
			reader, err := NewInstalledPackHistoryReader(target, options)
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.FindHistory(context.Background(), HistoryQuery{AlertName: "CorrTestOperationalFlood"})
			if err != nil || got == nil || len(got.Records) != 1 {
				t.Fatalf("actual public schema refused %v", err)
			}
			if got.Records[0].ID != "00000000-0000-4000-8000-000000000001" || got.Records[0].Labels["source_id"] != "source-1" || !bytes.Equal(got.Pages[0], raw) {
				t.Fatal("actual uppercase ID/Label/Value or raw page lost")
			}
		})
	}
}

func TestInstalledHistoryRefusesNestedSchemaAliases(t *testing.T) {
	raw, err := os.ReadFile("testdata/installed-history-page.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []struct{ old, next string }{
		{`"Label":`, `"label":`}, {`"Value":`, `"value":`},
		{`"Value":"source-1"`, `"Value":null`},
		{`"Label":"source_id"`, `"Label":123`},
		{`"annotations":[]`, `"annotations":[{"annotation":"summary","Value":"text"}]`},
		{`"annotations":[]`, `"annotations":[{"Annotation":"summary","value":"text"}]`},
	} {
		t.Run(alias.next, func(t *testing.T) {
			changed := bytes.Replace(raw, []byte(alias.old), []byte(alias.next), 1)
			if bytes.Equal(changed, raw) {
				t.Fatal("counterexample not applied")
			}
			if err := validateInstalledHistoryPage(changed); err == nil {
				t.Fatal("noncanonical nested schema accepted")
			}
		})
	}
}

func TestInstalledHistoryPreservesCanonicalNestedOptionalFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/installed-history-page.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"Value":"source-1"`), []byte(`"Value":"source-1","value_truncated":true,"value_length":1234`), 1)
	raw = bytes.Replace(raw, []byte(`"annotations":[]`), []byte(`"annotations":[{"Annotation":"summary","Value":"text"}]`), 1)
	if err := validateInstalledHistoryPage(raw); err != nil {
		t.Fatal(err)
	}
	// This is only valid raw representation, not complete source identity: an
	// oracle must reject truncated identity values rather than infer their suffix.
}
