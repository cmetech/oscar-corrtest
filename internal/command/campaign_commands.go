package command

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// campaignPollInterval is how often --wait and precheck poll status.
var campaignPollInterval = 10 * time.Second

type campaignClient struct {
	server, token string
	http          *http.Client
}

type campaignFlags struct {
	server, tokenFile, caFile, output *string
}

func addCampaignFlags(f *flag.FlagSet) campaignFlags {
	return campaignFlags{
		server:    f.String("server", "", "CorrTest base URL (required)"),
		tokenFile: f.String("token-file", "", "bearer token file (required)"),
		caFile:    f.String("ca-file", "", "CA bundle for the server certificate"),
		output:    f.String("output", "human", "human or json"),
	}
}

func (a *App) newCampaignClient(c campaignFlags) (*campaignClient, error) {
	if *c.server == "" || *c.tokenFile == "" || !validOutput(*c.output) {
		return nil, errors.New("--server and --token-file are required; --output must be human or json")
	}
	u, err := url.Parse(*c.server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("--server must be an http(s) URL")
	}
	tr := &http.Transport{}
	if *c.caFile != "" {
		pem, err := os.ReadFile(*c.caFile) // #nosec G304 -- operator-supplied CA path.
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("--ca-file contains no certificates")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &campaignClient{server: strings.TrimRight(*c.server, "/"), token: *c.tokenFile, http: &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// do sends one request; the token file is re-read every time and never printed.
func (c *campaignClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	tok, err := os.ReadFile(c.token) // #nosec G304 -- operator-supplied token path.
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

type apiFailure struct {
	status int
	code   string
	msg    string
}

func (e *apiFailure) Error() string {
	return fmt.Sprintf("server returned %d %s: %s", e.status, e.code, e.msg)
}

// call performs a JSON request and decodes {"data":...} into out.
func (c *campaignClient) call(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		return decodeAPIFailure(resp.StatusCode, raw)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return json.Unmarshal(env.Data, out)
}

func decodeAPIFailure(status int, raw []byte) *apiFailure {
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	return &apiFailure{status: status, code: e.Error.Code, msg: e.Error.Message}
}

func (a *App) runCampaign(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.commandUsageError("campaign", "missing campaign action")
	}
	switch args[0] {
	case "precheck":
		return a.campaignStart(ctx, "campaign precheck", args[1:], true)
	case "run":
		return a.campaignStart(ctx, "campaign run", args[1:], false)
	case "status":
		return a.campaignStatusCmd(ctx, args[1:])
	case "stop":
		return a.campaignStop(ctx, args[1:])
	case "compare":
		return a.campaignCompare(ctx, args[1:])
	case "export":
		return a.campaignExport(ctx, args[1:])
	default:
		return a.commandUsageError("campaign", fmt.Sprintf("unknown campaign action %q", args[0]))
	}
}

func (a *App) campaignFail(name string, err error) int {
	fmt.Fprintf(a.stderr, "%s: %v\n", name, err)
	var f *apiFailure
	if errors.As(err, &f) && f.status == http.StatusConflict {
		return 3
	}
	return 1
}

func terminalCampaign(s domain.CampaignStatus) bool {
	return s == domain.CampaignCompleted // INTERRUPTED continues to RECOVERING, then COMPLETED
}

func (a *App) campaignStart(ctx context.Context, name string, args []string, precheck bool) int {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	cf := addCampaignFlags(flags)
	scenario := flags.String("scenario", "alerting-day@v1", "scenario id@version: alerting-day@v1 or alerting-day-smoke@v1")
	ack := flags.String("ack", "", "comma-separated acknowledged warnings, e.g. A1,A3")
	runBy := flags.String("run-by", "human", "agent or human")
	wait := flags.Bool("wait", false, "follow status until the campaign finishes")
	if flags.Parse(args) != nil {
		return 2
	}
	if flags.NArg() != 0 || (*runBy != "agent" && *runBy != "human") {
		return a.commandUsageError(name, "unexpected arguments or invalid --run-by")
	}
	c, err := a.newCampaignClient(cf)
	if err != nil {
		return a.commandUsageError(name, err.Error())
	}
	body := map[string]any{"scenario": *scenario, "acknowledged": []string{}, "runBy": *runBy}
	if *ack != "" {
		body["acknowledged"] = strings.Split(*ack, ",")
	}
	path := "/api/v1/campaign/runs"
	if precheck {
		path = "/api/v1/campaign/prechecks"
	}
	var camp domain.Campaign
	if err := c.call(ctx, http.MethodPost, path, body, &camp); err != nil {
		return a.campaignFail(name, err)
	}
	if precheck || *wait {
		var view struct {
			Campaign domain.Campaign `json:"campaign"`
		}
		for {
			var perr error
			for try := 0; try < 3; try++ { // retry transient polling errors
				if perr = c.call(ctx, http.MethodGet, "/api/v1/campaign/runs/"+url.PathEscape(camp.ID), nil, &view); perr == nil {
					break
				}
				time.Sleep(campaignPollInterval)
			}
			if perr != nil {
				return a.campaignFail(name, perr)
			}
			if camp = view.Campaign; terminalCampaign(camp.Status) {
				break
			}
			select {
			case <-ctx.Done():
				fmt.Fprintf(a.stderr, "%s: %v\n", name, ctx.Err())
				return 1
			case <-time.After(campaignPollInterval):
			}
		}
	}
	code := 0
	if (precheck || *wait) && camp.Verdict != domain.VerdictPass {
		code = 4
		var rep struct {
			Startable *bool `json:"startable"`
		}
		_ = json.Unmarshal(camp.Report, &rep)
		if precheck || (rep.Startable != nil && !*rep.Startable) {
			code = 3 // precheck failure is a refusal, not a campaign verdict
		}
	}
	if *cf.output == "json" {
		if c := a.writeJSON(camp); c != 0 {
			return c
		}
		return code
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", camp.ID, camp.Status, displayEmpty(string(camp.Verdict)))
	return code
}

func (a *App) campaignStatusCmd(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("campaign status", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	cf := addCampaignFlags(flags)
	pos, perr := parseLeading(flags, args)
	if perr != nil {
		return 2
	}
	if len(pos) > 1 {
		return a.commandUsageError("campaign status", "expected at most one campaign id")
	}
	c, err := a.newCampaignClient(cf)
	if err != nil {
		return a.commandUsageError("campaign status", err.Error())
	}
	if len(pos) == 0 {
		var list []domain.Campaign
		if err := c.call(ctx, http.MethodGet, "/api/v1/campaign/runs", nil, &list); err != nil {
			return a.campaignFail("campaign status", err)
		}
		if *cf.output == "json" {
			return a.writeJSON(list)
		}
		for _, x := range list {
			fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", x.ID, x.Status, displayEmpty(string(x.Verdict)))
		}
		return 0
	}
	var view json.RawMessage
	if err := c.call(ctx, http.MethodGet, "/api/v1/campaign/runs/"+url.PathEscape(pos[0]), nil, &view); err != nil {
		return a.campaignFail("campaign status", err)
	}
	if *cf.output == "json" {
		return a.writeJSON(view)
	}
	var v struct {
		Campaign domain.Campaign `json:"campaign"`
	}
	_ = json.Unmarshal(view, &v)
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", v.Campaign.ID, v.Campaign.Status, displayEmpty(string(v.Campaign.Verdict)))
	return 0
}

func (a *App) campaignStop(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("campaign stop", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	cf := addCampaignFlags(flags)
	id, err := parseSinglePositional(flags, args)
	if err != nil {
		return 2
	}
	c, err := a.newCampaignClient(cf)
	if err != nil {
		return a.commandUsageError("campaign stop", err.Error())
	}
	var out map[string]string
	if err := c.call(ctx, http.MethodPost, "/api/v1/campaign/runs/"+url.PathEscape(id)+"/stop", map[string]string{}, &out); err != nil {
		return a.campaignFail("campaign stop", err)
	}
	if *cf.output == "json" {
		return a.writeJSON(out)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\n", out["id"], out["status"])
	return 0
}

func (a *App) campaignCompare(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("campaign compare", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	cf := addCampaignFlags(flags)
	pos, perr := parseLeading(flags, args)
	if perr != nil {
		return 2
	}
	if len(pos) < 2 {
		return a.commandUsageError("campaign compare", "expected two or more campaign ids")
	}
	c, err := a.newCampaignClient(cf)
	if err != nil {
		return a.commandUsageError("campaign compare", err.Error())
	}
	var out json.RawMessage
	if err := c.call(ctx, http.MethodGet, "/api/v1/campaign/compare?ids="+url.QueryEscape(strings.Join(pos, ",")), nil, &out); err != nil {
		var f *apiFailure
		if errors.As(err, &f) && f.status == http.StatusNotImplemented {
			fmt.Fprintln(a.stderr, "campaign compare: not implemented by this server yet")
			return 1
		}
		return a.campaignFail("campaign compare", err)
	}
	if *cf.output == "json" {
		return a.writeJSON(out)
	}
	fmt.Fprintln(a.stdout, string(out))
	return 0
}

func (a *App) campaignExport(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("campaign export", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	cf := addCampaignFlags(flags)
	outFile := flags.String("output-file", "", "new zip destination (required)")
	id, err := parseSinglePositional(flags, args)
	if err != nil || *outFile == "" {
		return a.commandUsageError("campaign export", "usage: campaign export <id> --output-file <zip>")
	}
	c, err := a.newCampaignClient(cf)
	if err != nil {
		return a.commandUsageError("campaign export", err.Error())
	}
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/campaign/runs/"+url.PathEscape(id)+"/bundle", nil)
	if err != nil {
		return a.campaignFail("campaign export", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return a.campaignFail("campaign export", decodeAPIFailure(resp.StatusCode, raw))
	}
	f, err := os.OpenFile(*outFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- operator-supplied destination.
	if err != nil {
		return a.campaignFail("campaign export", err)
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(*outFile)
		return a.campaignFail("campaign export", err)
	}
	if *cf.output == "json" {
		return a.writeJSON(map[string]any{"file": *outFile, "bytes": n})
	}
	fmt.Fprintf(a.stdout, "wrote %s (%d bytes)\n", *outFile, n)
	return 0
}

// parseLeading accepts positionals before or after flags.
func parseLeading(flags *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	return append(pos, flags.Args()...), nil
}
