// Build a runnable OCI image and push it to a registry, without Docker or any
// third-party library. Three blobs, one HTTP protocol, stdlib only.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	mediaManifest = "application/vnd.oci.image.manifest.v1+json"
	mediaConfig   = "application/vnd.oci.image.config.v1+json"
	mediaLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mediaIndex    = "application/vnd.oci.image.index.v1+json"

	// Fixed so two builds of the same input produce the same digest.
	epoch = "1970-01-01T00:00:00Z"
)

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

type imageConfig struct {
	Created      string `json:"created"`
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Entrypoint []string `json:"Entrypoint"`
	} `json:"config"`
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: %s build|push|explain [flags]", os.Args[0]))
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "build":
		fs := flag.NewFlagSet("build", flag.ExitOnError)
		bin := fs.String("bin", "out/hello", "file to place at / inside the image")
		out := fs.String("out", "out/image", "OCI layout directory to write")
		arch := fs.String("arch", "arm64", "image architecture")
		osName := fs.String("os", "linux", "image OS")
		fs.Parse(args)
		fail(build(*bin, *out, *osName, *arch))
	case "push":
		fs := flag.NewFlagSet("push", flag.ExitOnError)
		out := fs.String("out", "out/image", "OCI layout directory to read")
		reg := fs.String("registry", "http://localhost:5001", "registry base URL")
		repo := fs.String("repo", "hello", "repository name")
		tag := fs.String("tag", "v1", "tag")
		fs.Parse(args)
		fail(push(*out, *reg, *repo, *tag))
	case "explain":
		fs := flag.NewFlagSet("explain", flag.ExitOnError)
		out := fs.String("out", "out/image", "OCI layout directory to read")
		js := fs.String("json", "results/anatomy.json", "write the same data here")
		fs.Parse(args)
		fail(explain(*out, *js))
	default:
		fail(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

// build writes the three blobs plus the layout metadata an OCI image needs.
func build(bin, out, osName, arch string) error {
	payload, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(out, "blobs", "sha256"), 0o755); err != nil {
		return err
	}

	tarBytes, err := tarSingleFile("hello", payload)
	if err != nil {
		return err
	}
	gzBytes, err := gzipBytes(tarBytes)
	if err != nil {
		return err
	}
	layer := descriptor{MediaType: mediaLayer, Digest: digest(gzBytes), Size: int64(len(gzBytes))}
	if err := writeBlob(out, layer.Digest, gzBytes); err != nil {
		return err
	}

	var cfg imageConfig
	cfg.Created, cfg.Architecture, cfg.OS = epoch, arch, osName
	cfg.Config.Entrypoint = []string{"/hello"}
	cfg.RootFS.Type = "layers"
	// diff_id is the digest of the UNCOMPRESSED tar, which is why we hash both.
	cfg.RootFS.DiffIDs = []string{digest(tarBytes)}
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	cfgDesc := descriptor{MediaType: mediaConfig, Digest: digest(cfgBytes), Size: int64(len(cfgBytes))}
	if err := writeBlob(out, cfgDesc.Digest, cfgBytes); err != nil {
		return err
	}

	mf := manifest{SchemaVersion: 2, MediaType: mediaManifest, Config: cfgDesc, Layers: []descriptor{layer}}
	mfBytes, err := json.Marshal(mf)
	if err != nil {
		return err
	}
	mfDesc := descriptor{MediaType: mediaManifest, Digest: digest(mfBytes), Size: int64(len(mfBytes))}
	if err := writeBlob(out, mfDesc.Digest, mfBytes); err != nil {
		return err
	}

	idx := index{SchemaVersion: 2, MediaType: mediaIndex, Manifests: []descriptor{mfDesc}}
	idxBytes, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "index.json"), idxBytes, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		return err
	}

	fmt.Printf("layer    %s  %7d bytes (%d uncompressed)\n", layer.Digest, layer.Size, len(tarBytes))
	fmt.Printf("config   %s  %7d bytes\n", cfgDesc.Digest, cfgDesc.Size)
	fmt.Printf("manifest %s  %7d bytes\n", mfDesc.Digest, mfDesc.Size)
	fmt.Printf("wrote %s\n", out)
	return nil
}

func tarSingleFile(name string, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(data)),
		Format:   tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	// Close before Bytes: the trailer is written on Close, and Go evaluates
	// return values left to right.
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(in); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeBlob(out, dgst string, data []byte) error {
	return os.WriteFile(filepath.Join(out, "blobs", "sha256", strings.TrimPrefix(dgst, "sha256:")), data, 0o644)
}

func readBlob(out, dgst string) ([]byte, error) {
	return os.ReadFile(filepath.Join(out, "blobs", "sha256", strings.TrimPrefix(dgst, "sha256:")))
}

func readLayout(out string) (manifest, []byte, descriptor, error) {
	var idx index
	raw, err := os.ReadFile(filepath.Join(out, "index.json"))
	if err != nil {
		return manifest{}, nil, descriptor{}, err
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return manifest{}, nil, descriptor{}, err
	}
	if len(idx.Manifests) == 0 {
		return manifest{}, nil, descriptor{}, fmt.Errorf("%s: index has no manifests", out)
	}
	mfBytes, err := readBlob(out, idx.Manifests[0].Digest)
	if err != nil {
		return manifest{}, nil, descriptor{}, err
	}
	var mf manifest
	err = json.Unmarshal(mfBytes, &mf)
	return mf, mfBytes, idx.Manifests[0], err
}

// push walks the layout and speaks the registry v2 protocol: blobs first, then
// the manifest that names them.
func push(out, reg, repo, tag string) error {
	mf, mfBytes, _, err := readLayout(out)
	if err != nil {
		return err
	}
	for _, d := range append([]descriptor{mf.Config}, mf.Layers...) {
		data, err := readBlob(out, d.Digest)
		if err != nil {
			return err
		}
		if err := pushBlob(reg, repo, d.Digest, data); err != nil {
			return err
		}
	}
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", reg, repo, tag)
	req, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(mfBytes))
	req.Header.Set("Content-Type", mediaManifest)
	req.ContentLength = int64(len(mfBytes))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return httpErr("put manifest", resp)
	}
	fmt.Printf("pushed %s/%s:%s (%s)\n", strings.TrimPrefix(reg, "http://"), repo, tag, digest(mfBytes))
	return nil
}

func pushBlob(reg, repo, dgst string, data []byte) error {
	head, _ := http.NewRequest(http.MethodHead, fmt.Sprintf("%s/v2/%s/blobs/%s", reg, repo, dgst), nil)
	if resp, err := http.DefaultClient.Do(head); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			fmt.Printf("exists %s\n", dgst)
			return nil
		}
	}
	resp, err := http.Post(fmt.Sprintf("%s/v2/%s/blobs/uploads/", reg, repo), "", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return httpErr("start upload", resp)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "http") {
		loc = reg + loc
	}
	sep := "?"
	if strings.Contains(loc, "?") {
		sep = "&"
	}
	req, _ := http.NewRequest(http.MethodPut, loc+sep+"digest="+url.QueryEscape(dgst), bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(data))
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusCreated {
		return httpErr("upload blob", put)
	}
	fmt.Printf("pushed %s  %7d bytes\n", dgst, len(data))
	return nil
}

func httpErr(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s: %s: %s", what, resp.Status, strings.TrimSpace(string(body)))
}

func explain(out, jsonPath string) error {
	mf, mfBytes, mfDesc, err := readLayout(out)
	if err != nil {
		return err
	}
	cfgBytes, err := readBlob(out, mf.Config.Digest)
	if err != nil {
		return err
	}
	var cfg imageConfig
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return err
	}

	total := mfDesc.Size + mf.Config.Size
	fmt.Printf("%-9s %-72s %8s\n", "BLOB", "DIGEST", "BYTES")
	fmt.Printf("%-9s %-72s %8d\n", "manifest", mfDesc.Digest, mfDesc.Size)
	fmt.Printf("%-9s %-72s %8d\n", "config", mf.Config.Digest, mf.Config.Size)
	for _, l := range mf.Layers {
		total += l.Size
		fmt.Printf("%-9s %-72s %8d\n", "layer", l.Digest, l.Size)
	}
	fmt.Printf("\n%d blobs, %d bytes total, entrypoint %v, %s/%s\n",
		2+len(mf.Layers), total, cfg.Config.Entrypoint, cfg.OS, cfg.Architecture)

	if jsonPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return err
	}
	summary := map[string]any{
		"manifest":     mfDesc,
		"manifestJSON": json.RawMessage(mfBytes),
		"configJSON":   json.RawMessage(cfgBytes),
		"blobCount":    2 + len(mf.Layers),
		"totalBytes":   total,
	}
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(jsonPath, b, 0o644)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
