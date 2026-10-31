package testpack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The fixed project has three directories and two regular members. Decompress
// once under a total bound; parsing never extracts files or follows archive links.
func (k *Kit) readArchives() error {
	compressed := bytes.NewReader(k.blobs[projectArchive])
	g, e := gzip.NewReader(compressed)
	if e != nil {
		return e
	}
	g.Multistream(false)
	expanded, e := io.ReadAll(io.LimitReader(g, 4*maxBytes+1))
	ce := g.Close()
	if e != nil || ce != nil || len(expanded) > 4*maxBytes || compressed.Len() != 0 {
		return fmt.Errorf("project gzip bound/trailing data")
	}
	tr := tar.NewReader(bytes.NewReader(expanded))
	seen := map[string]bool{}
	regular := map[string][]byte{}
	directories := map[string]bool{packID + "/": true, packID + "/conf/": true, packID + "/conf/mappings/": true}
	for count := 0; ; count++ {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if count >= 5 || seen[h.Name] || h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 || h.Linkname != "" {
			return fmt.Errorf("invalid project member")
		}
		seen[h.Name] = true
		switch h.Typeflag {
		case tar.TypeDir:
			if !directories[h.Name] || h.Size != 0 {
				return fmt.Errorf("unexpected project directory")
			}
		case tar.TypeReg:
			if (h.Name != packID+"/pack.yml" && h.Name != packID+"/"+mappingPath) || h.Size <= 0 || h.Size > maxBytes {
				return fmt.Errorf("unexpected project file")
			}
			raw, e := io.ReadAll(io.LimitReader(tr, maxBytes+1))
			if e != nil || int64(len(raw)) != h.Size {
				return fmt.Errorf("project size mismatch")
			}
			regular[h.Name] = raw
		default:
			return fmt.Errorf("nonregular project member")
		}
	}
	if len(seen) != 5 || len(regular) != 2 {
		return fmt.Errorf("incomplete project")
	}
	// tar.Reader stops at end markers. Reject any nonzero data beyond them,
	// including appended alternate archives (padding from the actual builder is zero).
	used := int64(0)
	for _, raw := range regular {
		used += 512 + ((int64(len(raw))+511)/512)*512
	}
	used += 3 * 512
	if used+1024 > int64(len(expanded)) {
		return fmt.Errorf("missing tar end markers")
	}
	for _, b := range expanded[used:] {
		if b != 0 {
			return fmt.Errorf("trailing tar data")
		}
	}
	profileBytes, packBytes := regular[packID+"/"+mappingPath], regular[packID+"/pack.yml"]
	profile, e := parseJSON(profileBytes)
	if e != nil {
		return e
	}
	rows, ok := profile.([]any)
	if !ok || len(rows) != 1 {
		return fmt.Errorf("one Mapping required")
	}
	k.profile = encodeCanonical(rows[0])
	if digest(k.profile) != mappingSHA {
		return fmt.Errorf("fixed Mapping mismatch")
	}
	pack, e := strictYAML(packBytes)
	if e != nil {
		return e
	}
	expected := map[string]any{"schema_version": int64(1), "id": packID, "version": "0.1.0", "requires_oscar": ">=26.3.0,<27.0.0", "requires_tasks": map[string]any{"common": []any{}, "local": []any{}}, "contents": map[string]any{"jobs": int64(0), "tasks": []any{}, "fabfile": []any{}, "scripts": []any{}, "playbooks": []any{}, "rules": int64(0), "templates": int64(0)}, "archive": map[string]any{"file_count": int64(1), "expanded_bytes": int64(len(profileBytes))}, "files": map[string]any{mappingPath: map[string]any{"sha256": digest(profileBytes), "size": int64(len(profileBytes))}}}
	if !bytes.Equal(encodeCanonical(pack), encodeCanonical(expected)) {
		return fmt.Errorf("project manifest/schema/Mapping binding mismatch")
	}
	k.bindings.PackManifestSHA256 = digest(packBytes)
	content := append(bytes.Clone(packBytes), []byte(mappingPath)...)
	content = append(content, profileBytes...)
	k.bindings.PackContentHash = "sha256:" + digest(content)
	k.setMappingState(rows[0].(map[string]any))
	zr, e := zip.NewReader(bytes.NewReader(k.blobs["correlation-rules.zip"]), int64(len(k.blobs["correlation-rules.zip"])))
	if e != nil {
		return e
	}
	if len(zr.File) != 1 {
		return fmt.Errorf("one ZIP rule required")
	}
	f := zr.File[0]
	if f.Name != ruleName+".yml" || f.Method != zip.Store || !f.Mode().IsRegular() || f.Flags&1 != 0 || f.UncompressedSize64 == 0 || f.UncompressedSize64 > 262144 || f.CompressedSize64 != f.UncompressedSize64 {
		return fmt.Errorf("invalid fixed rule ZIP member")
	}
	if e := fixedZIPEnvelope(k.blobs["correlation-rules.zip"], f); e != nil {
		return e
	}
	rc, e := f.Open()
	if e != nil {
		return e
	}
	raw, e := io.ReadAll(io.LimitReader(rc, 262145))
	ce = rc.Close()
	if e != nil || ce != nil || uint64(len(raw)) != f.UncompressedSize64 {
		return fmt.Errorf("rule ZIP read bound/CRC mismatch")
	}
	rule, e := strictYAML(raw)
	if e != nil {
		return e
	}
	k.rule = encodeCanonical(rule)
	if digest(k.rule) != ruleSHA {
		return fmt.Errorf("fixed full authored rule mismatch")
	}
	return nil
}
func strictYAML(raw []byte) (any, error) {
	if len(raw) > maxBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid YAML bytes")
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	var n yaml.Node
	if e := d.Decode(&n); e != nil {
		return nil, e
	}
	var extra yaml.Node
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("multiple YAML documents")
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 {
		return nil, fmt.Errorf("invalid YAML document")
	}
	return yamlValue(n.Content[0], 0)
}
func yamlValue(n *yaml.Node, depth int) (any, error) {
	if depth > 32 || n.Anchor != "" || n.Alias != nil || n.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("YAML nesting/aliases forbidden")
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return nil, fmt.Errorf("unexpected YAML tag")
		}
		m := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Anchor != "" {
				return nil, fmt.Errorf("string YAML keys only")
			}
			if _, ok := m[k.Value]; ok {
				return nil, fmt.Errorf("duplicate YAML key")
			}
			v, e := yamlValue(n.Content[i+1], depth+1)
			if e != nil {
				return nil, e
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return nil, fmt.Errorf("unexpected YAML tag")
		}
		a := []any{}
		for _, c := range n.Content {
			v, e := yamlValue(c, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		return a, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			return n.Value, nil
		case "!!null":
			if n.Value == "null" {
				return nil, nil
			}
		case "!!bool":
			if n.Value == "true" {
				return true, nil
			}
			if n.Value == "false" {
				return false, nil
			}
		case "!!int":
			i, e := strconv.ParseInt(n.Value, 10, 64)
			if e == nil && strconv.FormatInt(i, 10) == n.Value && i >= -9007199254740991 && i <= 9007199254740991 {
				return i, nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported YAML scalar/type")
}

// ZIP readers deliberately allow self-extracting prefixes, trailing bytes and
// differing local names. The fixed kit has none: bind its one local record and
// central directory before treating the archive as exactly one safe member.
func fixedZIPEnvelope(raw []byte, f *zip.File) error {
	bad := func() error { return fmt.Errorf("unexpected rule ZIP envelope") }
	u16 := binary.LittleEndian.Uint16
	u32 := binary.LittleEndian.Uint32
	if len(raw) < 98 {
		return bad()
	}
	end := raw[len(raw)-22:]
	if u32(end) != 0x06054b50 || u16(end[4:]) != 0 || u16(end[6:]) != 0 || u16(end[8:]) != 1 || u16(end[10:]) != 1 || u16(end[20:]) != 0 {
		return bad()
	}
	offset, size := uint64(u32(end[16:])), uint64(u32(end[12:]))
	if offset+size != uint64(len(raw))-22 || size < 46 {
		return bad()
	}
	central := raw[offset : offset+size]
	nameLen := int(u16(central[28:]))
	extraLen := int(u16(central[30:]))
	commentLen := int(u16(central[32:]))
	if u32(central) != 0x02014b50 || u32(central[42:]) != 0 || extraLen != 0 || commentLen != 0 || 46+nameLen != len(central) || string(central[46:]) != f.Name {
		return bad()
	}
	if u32(raw) != 0x04034b50 || u16(raw[6:]) != f.Flags || f.Flags & ^uint16(8) != 0 || u16(raw[8:]) != zip.Store || u16(raw[28:]) != 0 || int(u16(raw[26:])) != len(f.Name) {
		return bad()
	}
	dataOffset := 30 + len(f.Name)
	if dataOffset > len(raw) || string(raw[30:dataOffset]) != f.Name {
		return bad()
	}
	dataEnd := uint64(dataOffset) + f.CompressedSize64
	if dataEnd > offset {
		return bad()
	}
	if f.Flags&8 != 0 {
		if dataEnd+16 != offset {
			return bad()
		}
		descriptor := raw[dataEnd:offset]
		if u32(descriptor) != 0x08074b50 || u32(descriptor[4:]) != f.CRC32 || uint64(u32(descriptor[8:])) != f.CompressedSize64 || uint64(u32(descriptor[12:])) != f.UncompressedSize64 {
			return bad()
		}
	} else if dataEnd != offset || u32(raw[14:]) != f.CRC32 || uint64(u32(raw[18:])) != f.CompressedSize64 || uint64(u32(raw[22:])) != f.UncompressedSize64 {
		return bad()
	}
	return nil
}
