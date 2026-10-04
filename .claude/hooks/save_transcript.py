import json
import shutil
import sys
from pathlib import Path

data = json.load(sys.stdin)
dest = Path(data["cwd"]) / ".chat-history"
dest.mkdir(parents=True, exist_ok=True)
shutil.copyfile(data["transcript_path"], dest / f"{data['session_id']}.jsonl")
