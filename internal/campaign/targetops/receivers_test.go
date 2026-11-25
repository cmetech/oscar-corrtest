package targetops

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/campaign/receivers"
)

// fakeMappings is the middleware mapping API: GET ?name= answers a list,
// GET ?mapping_namespace_name= a {rows} page, POST creates, DELETE /<id>.
type fakeMappings struct {
	mu   sync.Mutex
	maps map[string]mappingWire // id -> mapping
}

func (f *fakeMappings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const p = "/ext/mw/api/v1/mapping-data/mapping"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == p && r.URL.Query().Get("name") != "":
		out := []mappingWire{}
		for _, m := range f.maps {
			if m.Name == r.URL.Query().Get("name") {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && r.URL.Path == p:
		out := []mappingWire{}
		for _, m := range f.maps {
			if strings.EqualFold(m.Namespace, r.URL.Query().Get("mapping_namespace_name")) {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": out, "total": len(out)})
	case r.Method == http.MethodPost && r.URL.Path == p:
		var m mappingWire
		_ = json.NewDecoder(r.Body).Decode(&m)
		m.ID = "id-" + m.Name
		m.Namespace = strings.ToLower(m.Namespace) // OSCAR stores it lower case
		f.maps[m.ID] = m
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(m)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, p+"/"):
		delete(f.maps, strings.TrimPrefix(r.URL.Path, p+"/"))
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func TestReceiverMappingLifecycle(t *testing.T) {
	f := &fakeMappings{maps: map[string]mappingWire{"other": {ID: "other", Name: receivers.TrapOID, Namespace: "ext_other"}}}
	rx := Receivers{C: testClient(t, f)}
	ctx := context.Background()
	if _, found, err := rx.Mapping(ctx, receivers.TrapNamespace, receivers.TrapOID); found || err != nil {
		t.Fatalf("a same-named mapping in another namespace was found: %v", err)
	}
	if err := rx.CreateMapping(ctx, receivers.TrapNamespace, receivers.TrapOID, "d", receivers.TrapMapping()); err != nil {
		t.Fatal(err)
	}
	els, found, err := rx.Mapping(ctx, receivers.TrapNamespace, receivers.TrapOID)
	if !found || err != nil || !reflect.DeepEqual(els, receivers.TrapMapping()) {
		t.Fatalf("read-back %v %v %v", els, found, err)
	}
	if err := rx.CreateMapping(ctx, receivers.ProfileNamespace, "ct-rx-abc", "d", receivers.WebhookProfile()); err != nil {
		t.Fatal(err)
	}
	if names, err := rx.MappingNames(ctx, receivers.ProfileNamespace, "ct-rx-"); err != nil || !reflect.DeepEqual(names, []string{"ct-rx-abc"}) {
		t.Fatalf("names %v %v", names, err)
	}
	for _, m := range [][2]string{{receivers.TrapNamespace, receivers.TrapOID}, {receivers.ProfileNamespace, "ct-rx-abc"}, {receivers.ProfileNamespace, "ct-rx-abc"}} {
		if err := rx.DeleteMapping(ctx, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.maps) != 1 {
		t.Fatalf("left %v", f.maps)
	}
}
