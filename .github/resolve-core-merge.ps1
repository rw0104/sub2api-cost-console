param(
    [Parameter(Mandatory = $true)]
    [string]$Path
)

$ErrorActionPreference = 'Stop'
$reviewedPaths = @(
    'backend/internal/service/account_test_service.go',
    'backend/internal/service/ratelimit_service.go'
)
if ($Path -notin $reviewedPaths) {
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
    # git merge-file reports the number of conflict regions for these reviewed
    # paths (one in account_test_service.go, two in ratelimit_service.go).
    if ($mergeExitCode -gt 2) {
        throw "Git three-way merge failed for $Path with exit code $mergeExitCode"
    }

    $merged = [IO.File]::ReadAllText($oursPath)
    $pattern = '(?ms)^<<<<<<<[^\r\n]*\r?\n(?<ours>.*?)^\|\|\|\|\|\|\|[^\r\n]*\r?\n(?<base>.*?)^=======\r?\n(?<theirs>.*?)^>>>>>>>[^\r\n]*\r?\n'
    $matches = @([Text.RegularExpressions.Regex]::Matches($merged, $pattern))

    if ($Path -eq 'backend/internal/service/account_test_service.go') {
        if ($matches.Count -ne 1) {
            throw "The reviewed account-test merge shape changed; leaving the conflict blocked"
        }
        $match = $matches[0]
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
    } else {
        if ($matches.Count -ne 2) {
            throw "The reviewed rate-limit merge shape changed; leaving the conflict blocked"
        }
        $first = $matches[0]
        $second = $matches[1]
        if ($first.Groups['ours'].Value -notmatch 'confirmed terminal failure' -or
            $first.Groups['theirs'].Value -notmatch 'Team.*联动熔断') {
            throw "The reviewed rate-limit scheduling merge content changed; leaving the conflict blocked"
        }
        if ($second.Groups['theirs'].Value -notmatch '国产供应商' -or
            $second.Groups['theirs'].Value -notmatch 'deactivated_workspace') {
            throw "The reviewed rate-limit payment merge content changed; leaving the conflict blocked"
        }

        $firstReplacement = [string]::Join("`n", @(
            "`t// Team 联动熔断必须先于池模式/自定义错误码/临时不可调度的各类早退；"
            "`t// 同请求内与 fastpath 调用点的重复触发由方法内去重吸收。"
            "`ts.maybeHandleOpenAITeamLinkedError(ctx, account, statusCode, responseBody)"
            "`t// A deactivated OpenAI workspace is a confirmed terminal failure. Evaluate"
            "`t// it before pool/custom/temporary policies so no local rule can keep a dead"
            "`t// K12/Team workspace active or hide its impairment loss."
            "`tif statusCode == http.StatusPaymentRequired && account.Platform == PlatformOpenAI && isOpenAIWorkspaceDeactivated(responseBody) {"
            "`t`tmsg := `"Workspace deactivated (402): workspace has been deactivated`""
            "`t`tif upstreamMsg := strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(responseBody))); upstreamMsg != `"`" {"
            "`t`t`tmsg = `"Workspace deactivated (402): `" + upstreamMsg"
            "`t`t}"
            "`t`ts.handleTerminalAccountFailure(ctx, account, TerminalFailure{"
            "`t`t`tReason: TerminalFailureWorkspaceDeactivated, StatusCode: http.StatusPaymentRequired,"
            "`t`t`tUpstreamCode: `"deactivated_workspace`", Message: msg,"
            "`t`t}, msg)"
            "`t`treturn true"
            "`t}"
        )) + "`n"
        $secondReplacement = [string]::Join("`n", @(
            "`t// 国产供应商：余额不足是可恢复状态（充值/检测恢复后由周期任务自动解除），"
            "`t// 不能走 handleAuthError 永久置 status=error。改为可恢复的临时停调。"
            "`tif account.IsCNProvider() {"
            "`t`ts.handleCNProviderInsufficientBalance(ctx, account, upstreamMsg)"
            "`t`tshouldDisable = true"
            "`t`tbreak"
            "`t}"
            "`t// OpenAI: deactivated_workspace 表示工作区已停用，直接标记 error"
            "`tif account.Platform == PlatformOpenAI && gjson.GetBytes(responseBody, `"detail.code`").String() == `"deactivated_workspace`" {"
            "`t`tmsg := `"Workspace deactivated (402): workspace has been deactivated`""
            "`t`ts.handleAuthError(ctx, account, msg)"
            "`t`tshouldDisable = true"
            "`t`tbreak"
            "`t}"
        )) + "`n"
        # Replace from the end so the first match's original index stays valid.
        $merged = $merged.Remove($second.Index, $second.Length).Insert($second.Index, $secondReplacement)
        $merged = $merged.Remove($first.Index, $first.Length).Insert($first.Index, $firstReplacement)
    }

    if ($merged -match '(?m)^<<<<<<<|^\|\|\|\|\|\|\||^=======|^>>>>>>>') {
        throw "Reviewed compatible-core merge still contains conflict markers"
    }
    if ($Path -eq 'backend/internal/service/account_test_service.go' -and
        $merged -notmatch '(?m)^\s*testModelID = account\.GetMappedModel\(testModelID\)\r?\n\s*if mode == AccountTestModeCompact') {
        throw "Reviewed account-test merge produced an invalid compact routing block"
    }
    if ($Path -eq 'backend/internal/service/ratelimit_service.go' -and
        ($merged -notmatch 'maybeHandleOpenAITeamLinkedError' -or
         $merged -notmatch 'handleCNProviderInsufficientBalance')) {
        throw "Reviewed rate-limit merge dropped a required upstream branch"
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
