param([switch]$LinuxBuild)
$ErrorActionPreference = 'Stop'
$taskRepo = Split-Path -Parent $PSScriptRoot
Push-Location -LiteralPath $taskRepo
try {
    $unformatted = & gofmt -l ./cmd ./internal
    if ($LASTEXITCODE -ne 0) { throw 'gofmt failed' }
    if ($unformatted) { throw "Go files require formatting: $unformatted" }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    & python scripts/test_check_public_tree.py
    if ($LASTEXITCODE -ne 0) { throw 'Privacy checker tests failed' }
    & python scripts/check_public_tree.py
    if ($LASTEXITCODE -ne 0) { throw 'Repository privacy check failed' }
    if ($LinuxBuild) {
        $savedGOOS, $savedGOARCH, $savedCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
        try {
            $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = 'linux', 'amd64', '0'
            New-Item -ItemType Directory -Force -Path .dist | Out-Null
            & go build -trimpath -o .dist/sbmgr-check-linux-amd64 ./cmd/sbmgr
            if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }
        } finally {
            $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $savedGOOS, $savedGOARCH, $savedCGO
        }
    }
} finally {
    Pop-Location
}
