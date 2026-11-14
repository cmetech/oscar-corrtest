package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	campaignscenario "github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
)

const campaignAPIVersion = "corrtest.oscar/v1alpha1"

type campaignController interface {
	StartCampaign(context.Context, appruntime.CampaignOptions) (domain.Campaign, error)
	StopCampaign(context.Context, string) error
	CampaignStatus(context.Context, string) (appruntime.CampaignView, error)
	ListCampaigns(context.Context, domain.CampaignFilter) ([]domain.Campaign, error)
}

type campaignExporter interface {
	ExportCampaign(context.Context, string, string) (evidence.Result, error)
}

var _ campaignExporter = (*appruntime.Runtime)(nil)

// Compile-time guard: a signature drift would otherwise silently 404 every campaign route.
var _ campaignController = (*appruntime.Runtime)(nil)

// campaignWorker is implemented by the campaign worker manager (internal/campaign/worker).
type campaignWorker interface {
	Info(context.Context) workerapi.Info
	Start(context.Context, workerapi.LaneSpec) (workerapi.LaneStatus, error)
	Heartbeat(context.Context, string) (workerapi.LaneStatus, error)
	Stop(context.Context, string) (workerapi.LaneStatus, error)
	ResolveAll(context.Context, string) (workerapi.ResolveResult, error)
	Status(context.Context, string) (workerapi.LaneStatus, error)
	Ledger(context.Context, string, io.Writer) error
}

// Compile-time guard: the production data source must serve the worker routes.
var _ campaignWorker = (*appruntime.Runtime)(nil)

func apiData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"apiVersion": campaignAPIVersion, "data": data})
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// registerCampaignRoutes adds the JSON API. Every POST requires the Authorization
// header; a session cookie alone is rejected so browsers cannot drive campaigns.
func registerCampaignRoutes(mux *http.ServeMux, data DataSource, workerMode bool) {
	post := func(pattern string, h func(http.ResponseWriter, *http.Request, string)) {
		mux.HandleFunc("POST "+pattern, func(w http.ResponseWriter, r *http.Request) {
			id, ok := requestBearerIdentity(r)
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="oscar-corrtest"`)
				apiError(w, http.StatusUnauthorized, "bearer_required", "this route requires an Authorization: Bearer header")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			h(w, r, id)
		})
	}
	if ctl, ok := data.(campaignController); ok {
		start := func(precheckOnly bool) func(http.ResponseWriter, *http.Request, string) {
			return func(w http.ResponseWriter, r *http.Request, identity string) {
				var body struct {
					Scenario     string   `json:"scenario"`
					Acknowledged []string `json:"acknowledged"`
					RunBy        string   `json:"runBy"`
				}
				if !decodeStrict(w, r, &body) {
					return
				}
				s, ok := campaignscenario.Lookup(body.Scenario)
				if !ok {
					apiError(w, http.StatusBadRequest, "unknown_scenario", "scenario must be alerting-day@v1 or alerting-day-smoke@v1")
					return
				}
				if body.RunBy == "" {
					body.RunBy = "agent"
				}
				if body.RunBy != "agent" && body.RunBy != "human" {
					apiError(w, http.StatusBadRequest, "invalid_request", "runBy must be agent or human")
					return
				}
				// Prechecks reuse StartCampaign with PrecheckOnly; the campaign engine (C10)
				// runs the pre-check suite and completes without starting load.
				c, err := ctl.StartCampaign(r.Context(), appruntime.CampaignOptions{
					Scenario: s, Trigger: "cli", TriggerIdentity: identity, RunBy: body.RunBy,
					Acknowledged: body.Acknowledged, PrecheckOnly: precheckOnly,
				})
				if errors.Is(err, appruntime.ErrCampaignActive) {
					apiError(w, http.StatusConflict, "campaign_active", err.Error())
					return
				}
				if err != nil {
					apiError(w, http.StatusInternalServerError, "start_failed", err.Error())
					return
				}
				apiData(w, http.StatusAccepted, c)
			}
		}
		post("/api/v1/campaign/prechecks", start(true))
		post("/api/v1/campaign/runs", start(false))
		post("/api/v1/campaign/runs/{id}/stop", func(w http.ResponseWriter, r *http.Request, _ string) {
			err := ctl.StopCampaign(r.Context(), r.PathValue("id"))
			switch {
			case errors.Is(err, appruntime.ErrCampaignNotActive):
				apiError(w, http.StatusNotFound, "campaign_not_active", err.Error())
			case err != nil:
				apiError(w, http.StatusInternalServerError, "stop_failed", err.Error())
			default:
				// Report the resulting status (STOPPING, or INTERRUPTED from PRECHECK).
				view, err := ctl.CampaignStatus(r.Context(), r.PathValue("id"))
				if err != nil {
					apiError(w, http.StatusInternalServerError, "status_failed", err.Error())
					return
				}
				apiData(w, http.StatusOK, map[string]string{"id": view.Campaign.ID, "status": string(view.Campaign.Status)})
			}
		})
		mux.HandleFunc("GET /api/v1/campaign/runs", func(w http.ResponseWriter, r *http.Request) {
			list, err := ctl.ListCampaigns(r.Context(), domain.CampaignFilter{Limit: 100})
			if err != nil {
				apiError(w, http.StatusInternalServerError, "list_failed", err.Error())
				return
			}
			apiData(w, http.StatusOK, list)
		})
		mux.HandleFunc("GET /api/v1/campaign/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
			view, err := ctl.CampaignStatus(r.Context(), r.PathValue("id"))
			if errors.Is(err, appruntime.ErrCampaignNotFound) {
				apiError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			if err != nil {
				apiError(w, http.StatusInternalServerError, "status_failed", err.Error())
				return
			}
			apiData(w, http.StatusOK, view)
		})
		mux.HandleFunc("GET /api/v1/campaign/compare", func(w http.ResponseWriter, r *http.Request) {
			ids := splitIDs(r.URL.Query().Get("ids"))
			if len(ids) < 2 {
				apiError(w, http.StatusBadRequest, "invalid_request", "ids must list two or more campaign ids")
				return
			}
			res, err := compareCampaigns(r.Context(), ctl, ids)
			if errors.Is(err, appruntime.ErrCampaignNotFound) {
				apiError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			if err != nil {
				apiError(w, http.StatusInternalServerError, "compare_failed", err.Error())
				return
			}
			apiData(w, http.StatusOK, res)
		})
		mux.HandleFunc("GET /api/v1/campaign/runs/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requestBearerIdentity(r); !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="oscar-corrtest"`)
				apiError(w, http.StatusUnauthorized, "bearer_required", "this route requires an Authorization: Bearer header")
				return
			}
			x, ok := data.(campaignExporter)
			if !ok {
				apiError(w, http.StatusNotFound, "not_implemented", "campaign bundle download is unavailable")
				return
			}
			writeCampaignBundle(w, r, x, r.PathValue("id"))
		})
	}
	if wk, ok := data.(campaignWorker); ok && workerMode {
		registerWorkerRoutes(mux, wk, post)
	}
}

func registerWorkerRoutes(mux *http.ServeMux, wk campaignWorker, post func(string, func(http.ResponseWriter, *http.Request, string))) {
	const base = "/api/v1/campaign-worker"
	laneErr := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, workerapi.ErrLaneNotFound):
			apiError(w, http.StatusNotFound, "lane_not_found", err.Error())
		case errors.Is(err, workerapi.ErrLaneExists):
			apiError(w, http.StatusConflict, "lane_exists", err.Error())
		default:
			apiError(w, http.StatusInternalServerError, "worker_error", err.Error())
		}
	}
	mux.HandleFunc("GET "+base+"/info", func(w http.ResponseWriter, r *http.Request) {
		info := wk.Info(r.Context())
		apiData(w, http.StatusOK, map[string]any{"version": info.Version, "now": info.Now.UTC().Format("2006-01-02T15:04:05.000Z07:00"), "diskFreeBytes": info.DiskFreeBytes})
	})
	post(base+"/lanes", func(w http.ResponseWriter, r *http.Request, _ string) {
		var spec workerapi.LaneSpec
		if !decodeStrict(w, r, &spec) {
			return
		}
		if spec.ID == "" {
			apiError(w, http.StatusBadRequest, "invalid_request", "lane id is required")
			return
		}
		st, err := wk.Start(r.Context(), spec)
		if err != nil {
			laneErr(w, err)
			return
		}
		apiData(w, http.StatusAccepted, st)
	})
	mux.HandleFunc("GET "+base+"/lanes/{id}", func(w http.ResponseWriter, r *http.Request) {
		st, err := wk.Status(r.Context(), r.PathValue("id"))
		if err != nil {
			laneErr(w, err)
			return
		}
		apiData(w, http.StatusOK, st)
	})
	post(base+"/lanes/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request, _ string) {
		st, err := wk.Heartbeat(r.Context(), r.PathValue("id"))
		if err != nil {
			laneErr(w, err)
			return
		}
		apiData(w, http.StatusOK, st) // lane state and last error (ruling C34a)
	})
	post(base+"/lanes/{id}/stop", func(w http.ResponseWriter, r *http.Request, _ string) {
		st, err := wk.Stop(r.Context(), r.PathValue("id"))
		if err != nil {
			laneErr(w, err)
			return
		}
		apiData(w, http.StatusOK, st)
	})
	post(base+"/lanes/{id}/resolve-all", func(w http.ResponseWriter, r *http.Request, _ string) {
		res, err := wk.ResolveAll(r.Context(), r.PathValue("id")) // the worker detaches it from the request
		if err != nil {
			laneErr(w, err)
			return
		}
		apiData(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET "+base+"/lanes/{id}/ledger", func(w http.ResponseWriter, r *http.Request) {
		// Streaming contract: NDJSON rows, then a terminal record {"type":"end","ok":true}
		// (or ok:false with an error message if the stream failed after bytes were
		// written). A stream with no end record is truncated.
		cw := &countingWriter{w: w}
		w.Header().Set("Content-Type", "application/x-ndjson")
		err := wk.Ledger(r.Context(), r.PathValue("id"), cw)
		switch {
		case err == nil:
			_, _ = io.WriteString(w, "{\"type\":\"end\",\"ok\":true}\n")
		case cw.n > 0:
			msg, _ := json.Marshal(err.Error())
			_, _ = io.WriteString(w, "{\"type\":\"end\",\"ok\":false,\"error\":"+string(msg)+"}\n")
		default:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			laneErr(w, err)
		}
	})
}

type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apiError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB")
			return false
		}
		apiError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
