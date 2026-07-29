from pathlib import Path
import re

# webdav service_test
p = Path("internal/webdavsync/service_test.go")
t = p.read_text(encoding="utf-8")
t2 = re.sub(
    r'service\.Sync\(([^,]+),\s*([^)]+)\)',
    r'service.Sync(\1, \2, SyncModeIncremental)',
    t,
)
# avoid double-replace
t2 = t2.replace(", SyncModeIncremental, SyncModeIncremental)", ", SyncModeIncremental)")
if "ImportWithOptions" not in t2:
    # ensure fake has method - read current fake
    pass
p.write_text(t2, encoding="utf-8")
print("service_test Sync calls fixed", t2.count("SyncModeIncremental"))

# any other Sync( callers
for path in Path(".").rglob("*.go"):
    if "webdavsync" not in str(path) and "httpapi" not in str(path):
        continue
    text = path.read_text(encoding="utf-8")
    if ".Sync(ctx," in text or ".Sync(r.Context()" in text:
        print("check", path)
