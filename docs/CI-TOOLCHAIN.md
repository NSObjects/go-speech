# Reusable CI toolchain

GitHub-hosted runners are ephemeral: installing `ffmpeg`, `espeak-ng`,
`libopus-dev` and OpenCORE-AMR development headers for every job is slow
and sometimes fails due to Ubuntu apt mirror availability.

The versioned `ci/Dockerfile` creates a reusable Ubuntu 24.04 image with
audio codecs, speech-fixture generators, native headers, and build tools.
The `Build ASR CI Toolchain Image` workflow:

1. Builds the image on relevant pull requests **without publishing** it.
2. Checks all required command-line tools and native library headers.
3. Publishes `ghcr.io/nsobjects/go-speech-ci:ubuntu24-v1` on a change
   to the Dockerfile or build workflow merged to `main`.
4. Also publishes a `sha-<commit>` tag for a traceable build.
5. Uses GitHub Actions' BuildKit layer cache so image rebuilds do not
   download the same OS packages again unless Dockerfile layers change.

**This is a staged change:** existing ASR provider and Go workflows still
use their current runners. Do not switch them to `container: ...` until
the image workflow is green and the package is available to those jobs.

When migrating a job, set `permissions: packages: read` and use
`container.image: ghcr.io/nsobjects/go-speech-ci:ubuntu24-v1` plus
`container.credentials` containing the repository `GITHUB_TOKEN`
if the GHCR package is private. Remove system apt steps from migrated jobs.
Keep `actions/setup-go` with `cache: true` to cache Go module and build
artifacts independently from the OS image.

The image avoids installing apt packages **during test jobs** but jobs
still pull Docker image layers on each fresh hosted runner. Image layers
should be measured against the old apt installation timings; an image
cannot guarantee that a cold network pull is always faster.

Audio fixture cache in the Aliyun/Volcengine workflows is separate and
remains useful even after the toolchain image is adopted.
