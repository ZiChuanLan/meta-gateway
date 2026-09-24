<#
.SYNOPSIS
  Builds the artifacts that make this plugin installable from the plugin market.

.DESCRIPTION
  The host's github-release installer looks for an asset named
  "{id}_{version}_{goos}_{goarch}.zip" plus a "checksums.txt", and downloads the
  one matching its OWN goos/goarch — see internal/plugins/market_install.go. The
  platforms that matter are therefore the ones the gateway runs on, so
  linux/amd64 and linux/arm64 cover both container architectures.

  Every zip carries the plugin.json the host reads (asked from the binary itself
  via -dump-manifest, so there is no second copy in the repo to drift) next to
  the binary the manifest declares as its entrypoint. That pairing is what makes
  a market install work with no manual step: the host unpacks the package, reads
  the manifest, and starts the entrypoint on the port it reserved.

.EXAMPLE
  ./build-release.ps1 -Version 1.0.0
  # upload dist/* to the GitHub release tagged v1.0.0
#>
[CmdletBinding()]
param(
	[Parameter(Mandatory = $true)][string]$Version,
	[string[]]$Targets = @("linux/amd64", "linux/arm64", "windows/amd64", "darwin/arm64")
)

$ErrorActionPreference = "Stop"
$id = "jev-router"
$root = $PSScriptRoot
$dist = Join-Path $root "dist"
$stage = Join-Path $dist "_stage"

Remove-Item -Recurse -Force $dist -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $dist, $stage | Out-Null

# A host binary is needed to ask for the manifest. Building it here keeps the
# script runnable on a clean checkout.
$hostBinary = Join-Path $stage "$id-host.exe"
Push-Location $root
go build -o $hostBinary .
Pop-Location
if ($LASTEXITCODE -ne 0) { throw "host build failed" }

# The manifest comes from the plugin, not from this script: one definition, and
# the packaged copy is guaranteed to be the one the service serves.
$manifest = & $hostBinary -dump-manifest
if ($LASTEXITCODE -ne 0) { throw "manifest dump failed" }
Set-Content -Path (Join-Path $stage "plugin.json") -Value $manifest -Encoding utf8NoBOM
Remove-Item -Force $hostBinary

$checksumLines = @()
foreach ($target in $Targets) {
	$parts = $target.Split("/")
	$goos = $parts[0]
	$goarch = $parts[1]
	$binaryName = if ($goos -eq "windows") { "$id.exe" } else { $id }
	$output = Join-Path $stage $binaryName
	Write-Host "building $goos/$goarch"

	$previousGoos = $env:GOOS
	$previousGoarch = $env:GOARCH
	$previousCgo = $env:CGO_ENABLED
	try {
		$env:GOOS = $goos
		$env:GOARCH = $goarch
		$env:CGO_ENABLED = "0"
		Push-Location $root
		go build -trimpath -ldflags "-s -w" -o $output .
		if ($LASTEXITCODE -ne 0) { throw "go build failed for $goos/$goarch" }
		Pop-Location
	} finally {
		$env:GOOS = $previousGoos
		$env:GOARCH = $previousGoarch
		$env:CGO_ENABLED = $previousCgo
	}

	$zipName = "${id}_${Version}_${goos}_${goarch}.zip"
	$zipPath = Join-Path $dist $zipName
	Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zipPath -Force
	Remove-Item -Force $output

	$hash = (Get-FileHash -Algorithm SHA256 -Path $zipPath).Hash.ToLower()
	$checksumLines += "$hash  $zipName"
}

# sha256sum format: the host reads "<hex>  <file>" per line.
Set-Content -Path (Join-Path $dist "checksums.txt") -Value ($checksumLines -join "`n") -Encoding utf8NoBOM
Remove-Item -Recurse -Force $stage
Get-ChildItem $dist | Format-Table Name, Length
