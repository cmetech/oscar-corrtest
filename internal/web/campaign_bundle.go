package web

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// writeCampaignBundle streams the campaign evidence zip; shared by the API and the UI download.
func writeCampaignBundle(w http.ResponseWriter, r *http.Request, x campaignExporter, id string) {
	dir, err := os.MkdirTemp("", "oscar-corrtest-campaign-*")
	if err != nil {
		apiError(w, http.StatusInternalServerError, "export_failed", "campaign export unavailable")
		return
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "campaign.zip")
	if _, err := x.ExportCampaign(r.Context(), id, dest); err != nil {
		apiError(w, http.StatusUnprocessableEntity, "export_failed", err.Error())
		return
	}
	f, err := os.Open(dest) // #nosec G304 -- server-generated file in a private temp dir.
	if err != nil {
		apiError(w, http.StatusInternalServerError, "export_failed", "campaign export unavailable")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="oscar-corrtest-campaign-`+id+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, f)
}
