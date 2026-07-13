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

Once this repo has a real remote on GitHub, pushing a `vX.Y.Z`-shaped
tag (`git push --tags` above) also triggers `.github/workflows/
release.yml` automatically: it runs the test suite as a safety gate
and then runs `goreleaser release --clean` in CI via the
`goreleaser-action`, using the repo's automatically-provided
`GITHUB_TOKEN`, so no local `goreleaser` invocation is required at all
for a normal release. The manual steps above remain valid too — they're
still how to do a local dry run (`--snapshot --skip=publish`), and they
still work for a real release for anyone releasing from a machine
outside CI. `.goreleaser.yaml`'s `release.disable` field is templated
so it only actually attempts to publish when running inside GitHub
Actions (`GITHUB_ACTIONS=true`); every other environment, including
local dry runs in a repo with no remote, keeps skipping the release
pipe exactly as before.

`.github/workflows/ci.yml` separately runs `gofmt`, `go vet`, a build,
and `go test -race` on every push to `main` and every pull request, plus
a lightweight build-only sanity check on `ubuntu-latest`, `macos-latest`,
and `windows-latest` (goreleaser cross-compiles for all three, so a
platform-specific compile break is worth catching before release time).

## License

No `LICENSE` file has been added to this repository yet — that's a
decision for the project owner to make. Until one exists, no license
is granted for use, so treat this repository as "all rights reserved"
by default.
