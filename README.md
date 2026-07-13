# officegrep

A ripgrep-style command-line search tool that searches plain text files
as well as MS Office documents (`.docx`, `.pptx`, `.xlsx`), streaming
through each document instead of loading it fully into memory.

```
officegrep [flags] PATTERN [PATH...]
```

If no `PATH` is given, the current directory is searched. `PATTERN` is
a regular expression by default; use `-F`/`--fixed-strings` for a
literal search. Run `officegrep --help` for the full flag reference
(context lines, `--type` format filtering, JSON output, and more).

## Installing

### From a release archive

Prebuilt, statically-linked binaries (no runtime dependencies) for
Linux, macOS, and Windows on amd64/arm64 are attached to each
[release](../../releases) once releases are being published. Download
the archive for your platform, extract it, and put the `officegrep`
binary on your `PATH`.

### From source

Requires Go (see `go.mod` for the minimum version). From the repo
root:

```sh
go build -o officegrep ./cmd/officegrep
```

This produces an `officegrep` binary in the current directory. Since
the project has no CGO dependencies, a plain build is already static;
you can drop `CGO_ENABLED=0` in explicitly for a fully hermetic build
that doesn't depend on a local C toolchain being available at all:

```sh
CGO_ENABLED=0 go build -o officegrep ./cmd/officegrep
```

Check the installed version with:

```sh
officegrep --version
```

(A source build reports `officegrep dev` since the version string is
only stamped in by the release process below.)

## Releasing (maintainers)

Releases are built with [goreleaser](https://goreleaser.com) from the
`.goreleaser.yaml` config at the repo root, which cross-compiles
static (`CGO_ENABLED=0`) binaries for linux/darwin/windows on
amd64/arm64, packages them into `.tar.gz` (`.zip` on Windows) archives
alongside this README and a LICENSE, and writes a `checksums.txt`.

To try the build locally without needing a git tag, a remote, or a
publish token:

```sh
goreleaser release --snapshot --clean --skip=publish
```

Artifacts land in `dist/` (gitignored).

To cut a real, published release, this repo will first need an actual
git remote (e.g. on GitHub) and a `GITHUB_TOKEN` with permission to
create releases on it — neither exists yet as of this writing. Once
they do, a release is just:

```sh
git tag vX.Y.Z
git push --tags
goreleaser release --clean
```

goreleaser picks the version up from the git tag and stamps it into
each binary (`officegrep --version`) via ldflags.

## License

No `LICENSE` file has been added to this repository yet — that's a
decision for the project owner to make. Until one exists, no license
is granted for use, so treat this repository as "all rights reserved"
by default.
