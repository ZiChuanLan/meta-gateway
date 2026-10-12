# Seeds the isolated Docker acceptance stack (docker-compose.audit.yml).
# Everything points at the local mock upstream, so no real site is touched and no
# real token is spent. Re-runnable: every create is idempotent by name.
#
#   docker compose -p mg-audit -f docker-compose.audit.yml up -d --build --wait
#   ./tools/audit-mock/seed.ps1
$ErrorActionPreference = "Stop"
$base = "http://127.0.0.1:4318/admin"
$h = @{ Authorization = "Bearer 123456" }
function J($o) { $o | ConvertTo-Json -Depth 6 -Compress }
function Call($method, $path, $body) {
  try {
    if ($body) { return Invoke-RestMethod -Method $method "$base$path" -Headers $h -ContentType application/json -Body (J $body) }
    return Invoke-RestMethod -Method $method "$base$path" -Headers $h
  } catch {
    $detail = $_.ErrorDetails.Message
    Write-Output "  ! $method $path -> $detail"
    return $null
  }
}

Write-Output "== operating mode =="
# Team mode with an owner named admin / 123456, which is the login the console
# walkthrough uses.
Call "PATCH" "/mode" @{ mode = "team" } | Out-Null
$owner = Call "POST" "/mode/owner" @{ username = "admin"; name = "管理员"; password = "123456" }
Write-Output "owner=$(if ($owner) { $owner.role } else { 'exists' })"

Write-Output "== site + channel =="
$sites = Invoke-RestMethod "$base/sites" -Headers $h
$site = @($sites) | Where-Object { $_.base_url -like "*mock-upstream*" } | Select-Object -First 1
if (-not $site) {
  $site = Call "POST" "/sites" @{ name = "本地模拟"; base_url = "http://mock-upstream:4319"; status = "enabled"; platform = "new-api" }
}
Write-Output "site=$($site.id)"

$creds = Invoke-RestMethod "$base/sites/$($site.id)/credentials" -Headers $h
$cred = @($creds) | Select-Object -First 1
if (-not $cred) {
  $cred = Call "POST" "/sites/$($site.id)/credentials" @{ kind = "api_key"; secret = "mock-upstream-only"; status = "enabled" }
}
Write-Output "credential=$($cred.id)"

$channels = Invoke-RestMethod "$base/channels" -Headers $h
$ch = @($channels) | Where-Object { $_.base_url -like "*mock-upstream*" } | Select-Object -First 1
if (-not $ch) {
  $ch = Call "POST" "/channels" @{
    name          = "本地模拟 · 默认组"
    site_id       = $site.id
    credential_id = $cred.id
    type_hint     = "new-api"
    base_url      = "http://mock-upstream:4319/v1"
    group_name    = "default"
    priority      = 10
    weight        = 100
    routing_mode  = "auto"
    status        = "enabled"
  }
}
Write-Output "channel=$($ch.id)"

Write-Output "== models + routes =="
Call "POST" "/discovery/channels/$($ch.id)/refresh" @{} | Out-Null
Start-Sleep -Seconds 5
$models = Invoke-RestMethod "$base/discovery/models?channel_id=$($ch.id)" -Headers $h
$routes = Invoke-RestMethod "$base/routes" -Headers $h
$existing = @{}
foreach ($r in $routes) { $existing[$r.model_pattern] = $true }
$created = 0
foreach ($m in $models) {
  if ($existing.ContainsKey($m.model_name)) { continue }
  Call "POST" "/routes" @{ model_pattern = $m.model_name; enabled = $true; routing_mode = "auto" } | Out-Null
  $created++
}
Write-Output "models=$(@($models).Count) routes_created=$created"

Write-Output "== downstream key =="
$keys = Invoke-RestMethod "$base/downstream-keys" -Headers $h
if (-not (@($keys) | Where-Object { $_.name -eq "本地验收令牌" })) {
  $k = Call "POST" "/downstream-keys" @{ name = "本地验收令牌"; enabled = $true }
  Write-Output "key=$($k.id)"
}

Write-Output "== team policy + accounts =="
$policies = Invoke-RestMethod "$base/team/policies" -Headers $h
$policy = @($policies) | Where-Object { $_.name -eq "验收策略" } | Select-Object -First 1
if (-not $policy) {
  $policy = Call "POST" "/team/policies" @{ name = "验收策略"; all_models = $true; max_keys = 5; rpm = 120; allow_routing = $true; allow_request_preferences = $true }
}
Write-Output "policy=$($policy.id)"
# Every account gets the policy, so a member actually sees the catalogue.
Call "PATCH" "/team/users/1" @{ policy_id = $policy.id } | Out-Null

$users = Invoke-RestMethod "$base/team/users" -Headers $h
$names = @{}
foreach ($u in $users) { $names[$u.username] = $true }
foreach ($spec in @(
  @{ username = "auditmember"; name = "验收成员"; password = "local-member-audit-only"; role = "member" },
  @{ username = "auditadmin"; name = "验收管理员"; password = "local-admin-audit-only"; role = "admin" }
)) {
  if ($names.ContainsKey($spec.username)) { continue }
  $made = Call "POST" "/team/users" @{ username = $spec.username; name = $spec.name; password = $spec.password; role = $spec.role; policy_id = $policy.id }
  if ($made) { Write-Output "user=$($made.user.id) $($spec.username)" }
}
Write-Output "== done =="
