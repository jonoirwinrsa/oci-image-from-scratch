# oci-image-from-scratch

**A runnable container image is three blobs and a JSON file.** 360 lines of Go, stdlib only —
no Docker, no buildkit, no `go-containerregistry`.

```
BLOB      DIGEST                                                                      BYTES
manifest  sha256:144a69037b6b224171ebbe8f61dea52e61ce483a4c7dc97a20741cdbfada6bdd       404
config    sha256:fdaff5e20c5f8aa5fbfa212c980a61ac6957bd5024a2b4a062ac8dc07829a96e       219
layer     sha256:9df749ad3d20c94ef4b28464662f00eaec58c1946938f9ae84606f87011bad27    666200
```

666 KB of that is the Go binary. The container format itself costs 623 bytes.

## Run it

```sh
make registry   # colima + registry:2 on :5001 (AirPlay owns 5000)
make all        # build, push, then docker run it
```

```
hello from an image built without docker
pid=1 linux/arm64 args=[/hello]
```

Also: `make image` (no daemon needed), `make explain`, `make down`, `make clean`.

## The three blobs

- **Layer** — a gzipped tar of the rootfs. One file, `/hello`, mode 0755. No base image, no libc,
  no shell; a static binary needs none of them.
- **Config** — architecture, OS, entrypoint, and `rootfs.diff_ids`. A layer is hashed twice: the
  manifest names the digest of the *compressed* layer, `diff_ids` names the *uncompressed* tar.
  Swapping those is the most common way a hand-built image fails to run, and nothing tells you.
- **Manifest** — a `{mediaType, digest, size}` descriptor per blob. Its own digest is the image ID.

Written out as an [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md):
`blobs/sha256/<hex>` plus `index.json`. That directory *is* the image.

## Pushing is four HTTP requests

`HEAD` the blob (already there? skip — this is why re-pushing a base layer is instant) → `POST`
an upload → `PUT` the bytes with `?digest=` → `PUT` the manifest last, since it may only name
blobs that already exist. That ordering is why a half-pushed image can never be pulled.

## Two things I got wrong

**A truncated layer pushes and pulls perfectly.** I closed the gzip writer *after* reading the
buffer (`return buf.Bytes(), zw.Close()` — Go evaluates left to right), losing the tar trailer and
gzip footer on every layer. The registry recomputed the digest and it matched, because it hashed
the same broken bytes. It died seconds later in the snapshotter: `incomplete deflate data`.
Content addressing proves you got the bytes the manifest named, not that they're a valid tar.

**`go build` inside a git repo isn't reproducible.** Digests were stable until `git init`, then
changed on every commit — Go stamps the VCS revision and a `dirty` flag into main packages.
`-buildvcs=false` fixes it. Fixed tar mtimes and a fixed `created` handle the rest; build twice,
get the same digest.

## Why

First of a series on container cold starts. Lazy pulling, chunked layers and nydus are all just
changes to *when* the bytes in blob #3 arrive — which is hard to see until an image is obviously
some addressable blobs behind an HTTP API.
