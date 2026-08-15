param(
    [Parameter(Mandatory = $true)]
    [string]$Path
)

$ErrorActionPreference = 'Stop'
$expectedPath = 'backend/internal/service/account_test_service.go'
if ($Path -ne $expectedPath) {
    throw "Unsupported compatible-core merge path: $Path"
}

$tempRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
$token = [Guid]::NewGuid().ToString('N')
$oursPath = Join-Path $tempRoot "core-merge-$token-ours.go"
$basePath = Join-Path $tempRoot "core-merge-$token-base.go"
$theirsPath = Join-Path $tempRoot "core-merge-$token-theirs.go"

function Get-GitBlobBytes([string]$Spec) {
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = 'git'
    $startInfo.Arguments = "cat-file blob `"$Spec`""
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    if (-not $process.Start()) {
        throw "Unable to read Git merge stage: $Spec"
    }
    $buffer = [IO.MemoryStream]::new()
    $process.StandardOutput.BaseStream.CopyTo($buffer)
    $errorText = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) {
        throw "Unable to read Git merge stage $Spec`: $errorText"
    }
    return $buffer.ToArray()
}

try {
    [IO.File]::WriteAllBytes($oursPath, (Get-GitBlobBytes ":2:$Path"))
    [IO.File]::WriteAllBytes($basePath, (Get-GitBlobBytes ":1:$Path"))
    [IO.File]::WriteAllBytes($theirsPath, (Get-GitBlobBytes ":3:$Path"))

    & git merge-file --diff3 $oursPath $basePath $theirsPath
    $mergeExitCode = $LASTEXITCODE
    if ($mergeExitCode -gt 1) {
        throw "Git three-way merge failed for $Path with exit code $mergeExitCode"
    }

    $merged = [IO.File]::ReadAllText($oursPath)
    $pattern = '(?ms)^<<<<<<<[^\r\n]*\r?\n(?<ours>.*?)^\|\|\|\|\|\|\|[^\r\n]*\r?\n(?<base>.*?)^=======\r?\n(?<theirs>.*?)^>>>>>>>[^\r\n]*\r?\n'
    $match = [Text.RegularExpressions.Regex]::Match($merged, $pattern)
    if (-not $match.Success -or ([Text.RegularExpressions.Regex]::Matches($merged, '^<<<<<<<', [Text.RegularExpressions.RegexOptions]::Multiline).Count -ne 1)) {
        throw "The reviewed account-test merge shape changed; leaving the conflict blocked"
    }

    $oursBlock = $match.Groups['ours'].Value
    $theirsBlock = $match.Groups['theirs'].Value
    if ($oursBlock -notmatch 'compact-only mapping on top' -or $theirsBlock -notmatch 'remote compaction v2') {
        throw "The reviewed account-test merge content changed; leaving the conflict blocked"
    }

    $replacement = [string]::Join("`n", @(
        "`t// account model mapping. Native remote compaction v2 uses the ordinary"
        "`t// /responses wire and does not apply legacy compact-only mapping."
        "`ttestModelID = account.GetMappedModel(testModelID)"
    )) + "`n"
    $merged = $merged.Remove($match.Index, $match.Length).Insert($match.Index, $replacement)
    if ($merged -match '(?m)^<<<<<<<|^\|\|\|\|\|\|\||^=======|^>>>>>>>') {
        throw "Reviewed account-test merge still contains conflict markers"
    }
    if ($merged -notmatch '(?m)^\s*testModelID = account\.GetMappedModel\(testModelID\)\r?\n\s*if mode == AccountTestModeCompact') {
        throw "Reviewed account-test merge produced an invalid compact routing block"
    }

    $utf8NoBom = [Text.UTF8Encoding]::new($false)
    [IO.File]::WriteAllText((Resolve-Path $Path), $merged, $utf8NoBom)
    & git add -- $Path
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to stage resolved compatible-core path: $Path"
    }
} finally {
    Remove-Item -LiteralPath $oursPath, $basePath, $theirsPath -Force -ErrorAction SilentlyContinue
}
