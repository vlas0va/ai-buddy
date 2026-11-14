"""Озвучка видео-демо: narration.json → scene_<id>.mp3 (голос Microsoft Edge, женский, бесплатно, без ключа).

Запуск из корня репозитория (нужен только Docker):
  docker run --rm -v "${PWD}/presentation/voice:/work" -w /work python:3.12-slim sh -c "pip install -q edge-tts && python tts.py"
"""

import asyncio
import json
from pathlib import Path

import edge_tts


async def main() -> None:
    cfg = json.loads(Path("narration.json").read_text(encoding="utf-8"))
    for scene in cfg["scenes"]:
        out = Path(f"scene_{scene['id']}.mp3")
        await edge_tts.Communicate(scene["text"], cfg["voice"], rate=cfg.get("rate", "+0%")).save(str(out))
        print("готово:", out)


asyncio.run(main())
