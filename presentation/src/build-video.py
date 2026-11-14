"""Склейка видео-демо с голосом и субтитрами: video/demo.webm + video/scenes.json (record-voiced.js)
+ ../voice/scene_<id>.mp3 (tts.py) → ../demo.mp4.

Каждая сцена длится не меньше своей озвучки: если запись короче, последний кадр замирает.
Запуск: python3 build-video.py  (нужен ffmpeg с libass)
"""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).parent
VIDEO = HERE / "video"
VOICE = HERE.parent / "voice"
OUT = HERE.parent / "demo.mp4"
LEAD_IN = 0.35  # пауза перед голосом в начале сцены, с
TAIL = 0.6  # пауза после голоса, с


def run(*args: str) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def duration(path: Path) -> float:
    return float(run("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", str(path)))


def ts(t: float) -> str:
    ms = int(round(t * 1000))
    return f"{ms // 3600000:02}:{ms // 60000 % 60:02}:{ms // 1000 % 60:02},{ms % 1000:03}"


def chunks(text: str, limit: int = 105) -> list[str]:
    """Делит текст субтитра на куски по предложениям, не длиннее limit символов, если получится."""
    parts, cur = [], ""
    for sentence in text.replace("! ", "!|").replace(". ", ".|").replace(": ", ":|").split("|"):
        if cur and len(cur) + 1 + len(sentence) > limit:
            parts.append(cur)
            cur = sentence
        else:
            cur = f"{cur} {sentence}".strip()
    if cur:
        parts.append(cur)
    return parts


def main() -> None:
    rec = json.loads((VIDEO / "scenes.json").read_text())
    narration = {s["id"]: s for s in json.loads((VOICE / "narration.json").read_text(encoding="utf-8"))["scenes"]}
    scenes = rec["scenes"]
    missing = [s["id"] for s in scenes if not (VOICE / f"scene_{s['id']}.mp3").exists()]
    if missing:
        sys.exit(f"нет озвучки: {missing} — запустите presentation/voice/tts.py")

    work = Path(tempfile.mkdtemp())
    parts, srt, clock = [], [], 0.0
    for i, s in enumerate(scenes):
        start = s["t"]
        # конец сцены чуть раньше начала следующей: стоп-кадр не должен поймать первый кадр следующего экрана
        end = scenes[i + 1]["t"] - 0.25 if i + 1 < len(scenes) else rec["end"]
        seg = end - start
        audio = VOICE / f"scene_{s['id']}.mp3"
        alen = duration(audio)
        target = max(seg, LEAD_IN + alen + TAIL)
        part = work / f"{i:02}.mp4"
        run("ffmpeg", "-y", "-loglevel", "error", "-ss", f"{start:.3f}", "-t", f"{seg:.3f}", "-i", str(VIDEO / "demo.webm"),
            "-i", str(audio), "-filter_complex",
            f"[0:v]fps=30,tpad=stop_mode=clone:stop_duration={target - seg + 0.1:.3f},trim=0:{target:.3f},setpts=PTS-STARTPTS[v];"
            f"[1:a]adelay={int(LEAD_IN * 1000)}:all=1,apad,atrim=0:{target:.3f},aresample=48000[a]",
            "-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p",
            "-c:a", "aac", "-b:a", "160k", "-ac", "2", str(part))
        parts.append(part)
        # субтитры: куски текста делят время озвучки пропорционально длине
        text = narration[s["id"]]["sub"]
        pieces = chunks(text)
        total = sum(len(p) for p in pieces)
        t = clock + LEAD_IN
        for p in pieces:
            d = alen * len(p) / total
            srt.append((t, t + d, p))
            t += d
        clock += target

    lst = work / "list.txt"
    lst.write_text("".join(f"file '{p}'\n" for p in parts))
    joined = work / "joined.mp4"
    run("ffmpeg", "-y", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", str(lst), "-c", "copy", str(joined))
    sub = work / "subs.srt"
    sub.write_text("".join(f"{n}\n{ts(a)} --> {ts(b)}\n{p}\n\n" for n, (a, b, p) in enumerate(srt, 1)), encoding="utf-8")
    (HERE.parent / "demo.srt").write_text(sub.read_text(encoding="utf-8"), encoding="utf-8")
    style = ("FontName=DejaVu Sans,FontSize=10,PrimaryColour=&H00FFFFFF,BackColour=&H30211814,BorderStyle=4,"
             "Outline=0,Shadow=0,MarginV=14,MarginL=40,MarginR=40")
    run("ffmpeg", "-y", "-loglevel", "error", "-i", str(joined), "-vf", f"subtitles={sub}:force_style='{style}'",
        "-c:v", "libx264", "-preset", "medium", "-crf", "21", "-pix_fmt", "yuv420p", "-c:a", "copy",
        "-movflags", "+faststart", str(OUT))
    print(f"{OUT} — {duration(OUT):.1f} с, субтитры: {HERE.parent / 'demo.srt'}")


if __name__ == "__main__":
    main()
