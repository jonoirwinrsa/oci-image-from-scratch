# oci-image-from-scratch

A container image builder in 362 lines of Go, standard library only. It writes the layer, config
and manifest by hand and pushes them to a registry, with no Docker, buildkit or
`go-containerregistry` involved.

```
BLOB      DIGEST                                                                      BYTES
manifest  sha256:144a69037b6b224171ebbe8f61dea52e61ce483a4c7dc97a20741cdbfada6bdd       404
config    sha256:fdaff5e20c5f8aa5fbfa212c980a61ac6957bd5024a2b4a062ac8dc07829a96e       219
layer     sha256:9df749ad3d20c94ef4b28464662f00eaec58c1946938f9ae84606f87011bad27    666200
```

666 KB of that is the Go binary. The container format itself costs 623 bytes.

Those digests are from go1.26.1 targeting linux/arm64. Another toolchain or architecture gives
different ones, just as deterministically.

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

## What gets built

The layer is a gzipped tar of the rootfs holding one file, `/hello`, mode 0755. There is no base
image under it, and no libc or shell either, because a static binary needs neither.

The config carries architecture, OS, entrypoint and `rootfs.diff_ids`. A layer gets hashed twice
here, which is the part worth slowing down for: the manifest names the digest of the compressed
layer, while `diff_ids` names the uncompressed tar. Swapping the two is an easy way to get a
broken image, and nothing tells you.

The manifest is a `{mediaType, digest, size}` descriptor per blob, and its own digest is the
image ID.

All of it lands in an [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md):
`blobs/sha256/<hex>` files plus `index.json`, and that directory is the image.

## The push protocol

Every blob gets a `HEAD` first, and the upload is skipped when the registry already has those
bytes, which is why re-pushing an unchanged base layer is instant. Otherwise it `POST`s an upload
and then `PUT`s the bytes with `?digest=` so the registry can verify them itself. The manifest
goes last, since it may only name blobs that already exist, which also means a half-pushed image
can never be pulled.

Builds are deterministic, with fixed tar mtimes, a fixed `created` timestamp, and
`-buildvcs=false` on the payload so Go doesn't stamp the git revision in. Two clean builds give
the same digests.
