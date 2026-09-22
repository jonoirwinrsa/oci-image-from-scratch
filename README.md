# oci-image-from-scratch

Builds a runnable container image and pushes it to a registry in 360 lines of Go, using nothing
but the standard library. No Docker, no buildkit, no `go-containerregistry`.

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

**Layer.** A gzipped tar of the rootfs. One file, `/hello`, mode 0755. No base image, no libc and
no shell, because a static binary needs none of them.

**Config.** Architecture, OS, entrypoint, and `rootfs.diff_ids`. A layer gets hashed twice: the
manifest names the digest of the compressed layer, while `diff_ids` names the uncompressed tar.
Swapping those is the most common way a hand-built image fails to run, and nothing tells you.

**Manifest.** A `{mediaType, digest, size}` descriptor per blob. Its own digest is the image ID.

All of it lands in an [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md):
`blobs/sha256/<hex>` files plus `index.json`. That directory is the image.

## The push protocol

`HEAD` the blob first and skip it if the registry already has those bytes, which is why re-pushing
an unchanged base layer is instant. Otherwise `POST` an upload, then `PUT` the bytes with
`?digest=` so the registry can verify them itself. The manifest goes last, because it may only
name blobs that already exist. That ordering is why a half-pushed image can never be pulled.

Builds are deterministic: fixed tar mtimes, a fixed `created` timestamp, and `-buildvcs=false` on
the payload so Go doesn't stamp the git revision into it. Same input, same digest.
