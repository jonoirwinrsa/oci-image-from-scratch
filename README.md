# oci-image-from-scratch

**A runnable container image is three blobs and a JSON file. Here it is in 360 lines of Go,
stdlib only — no Docker, no buildkit, no `go-containerregistry`.**

```
BLOB      DIGEST                                                                      BYTES
manifest  sha256:4f6573b2bf4def9210fb14ed2d67c807f8fc55c0f256a966853aa6d2b6466ba4       404
config    sha256:4c3ec8679a6f5808da847f1f7ba14fe336d01751579917115ebca92e47119022       219
layer     sha256:b8ee483797dccb8510c1fb7017f439d975c1a0347298c003e477d6911556d5f7    666110

3 blobs, 666733 bytes total, entrypoint [/hello], linux/arm64
```

That image pulls and runs under Docker like any other. The 666 KB is entirely the Go binary;
the container format itself costs 623 bytes.

## Run it

```sh
make registry   # colima + registry:2 on :5001
make all        # build the image, push it, then docker run it
```

```
$ make verify
hello from an image built without docker
pid=1 linux/arm64 args=[/hello]
```

Other targets: `make image` (build only, no daemon needed), `make explain` (the table above,
also written to `results/anatomy.json`), `make down`, `make clean`.

Port 5001, not 5000 — AirPlay Receiver owns 5000 on macOS.

## What the three blobs are

**The layer** is a tar of the filesystem, gzipped. One file, `/hello`, mode 0755. That is the
whole root filesystem: no base image, no libc, no shell. A static binary doesn't need them.

**The config** is the JSON that describes the runtime: architecture, OS, entrypoint, and
`rootfs.diff_ids`. Note that a layer gets hashed *twice* — the manifest references the digest of
the **compressed** layer (what the registry stores and transfers), while `diff_ids` lists the
digest of the **uncompressed** tar (what the runtime unpacks and what the layer cache is keyed
on). Getting these the wrong way round is the single most common way a hand-built image fails to
run, and the error message never tells you that.

**The manifest** ties them together: a descriptor for the config and one per layer, each a
`{mediaType, digest, size}` triple. Content addressing all the way down — the manifest digest is
the image ID, and it changes if any byte below it changes.

Those blobs are written into an [OCI image layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md):
`blobs/sha256/<hex>` files plus `index.json` and an `oci-layout` marker. That directory *is* the
image, portable to `skopeo`, `crane`, or this repo's pusher.

## Pushing is four HTTP requests

No magic here either — the [registry v2 API](https://github.com/opencontainers/distribution-spec)
is plain HTTP:

1. `HEAD /v2/hello/blobs/sha256:...` — already there? then skip it. This is why re-pushing an
   unchanged base layer is instant.
2. `POST /v2/hello/blobs/uploads/` → `202` with a `Location` to upload into.
3. `PUT <location>?digest=sha256:...` with the bytes. The registry verifies the digest itself.
4. `PUT /v2/hello/manifests/v1` with `Content-Type: application/vnd.oci.image.manifest.v1+json`,
   last, because the manifest may only name blobs that already exist.

Once step 4 lands, the tag is live. That ordering is the whole reason a half-pushed image can
never be pulled.

## Reproducible by construction

Build twice, get the same digest:

```sh
$ make image && make image
manifest sha256:4f6573b2bf4def9210fb14ed2d67c807f8fc55c0f256a966853aa6d2b6466ba4
manifest sha256:4f6573b2bf4def9210fb14ed2d67c807f8fc55c0f256a966853aa6d2b6466ba4
```

It falls out of two lines: fixed tar headers (zero mtime, no uname/gname) and a fixed `created`
timestamp in the config. Ordinary image builds embed the wall clock in both places, which is why
identical inputs normally yield a different image ID every time.

## The digest is not a checksum of anything useful

Building this the first time, the gzip writer was closed *after* the buffer was read, so every
layer was missing its tar trailer and gzip footer. The image still pushed cleanly, and still
pulled cleanly — the registry recomputes the digest and it matched perfectly, because the digest
was taken over the same truncated bytes. It failed several seconds later, in the snapshotter:

```
failed to extract layer ... unpigz: skipping: <stdin>: corrupted -- incomplete deflate data
```

Content addressing guarantees you got the bytes the manifest named. It says nothing about whether
those bytes are a valid tar. Everything up to unpack is happy to move a broken layer around at
full speed, which is worth remembering the next time a pull "succeeds" and the container dies on
start.

## Why this repo exists

First of a series on container cold starts. You can't reason about lazy pulling, chunked layers,
or nydus until you can see that an image is just addressable blobs behind an HTTP API — every
one of those techniques is a change to *when* the bytes in blob #3 arrive.
