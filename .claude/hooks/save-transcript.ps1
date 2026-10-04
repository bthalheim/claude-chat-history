$j = [Console]::In.ReadToEnd() | ConvertFrom-Json
$dir = Join-Path $j.cwd 'chat-history'
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item $j.transcript_path (Join-Path $dir ($j.session_id + '.jsonl')) -Force
