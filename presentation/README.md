# Презентация и видео

- `AI-buddy.pdf` — продуктовая презентация (20 слайдов, 16:9), ссылка на репозиторий — на первом слайде.
- `demo.mp4` — видео-демо с женским голосом и субтитрами (≈2 мин), `demo.srt` — субтитры отдельно.

Как пересобрать на свежем стеке (`make up` со stub-LLM, web на `http://127.0.0.1:5173`):

```bash
cd presentation/src
node screenshots.js && node screenshots-extras.js   # скриншоты в img/ (нужен Playwright)
node topdf.js                                       # deck.html → ../AI-buddy.pdf

# видео: запись → озвучка → склейка
node record-voiced.js                               # video/demo.webm + video/scenes.json
cd ../voice && pip install edge-tts && python tts.py   # scene_*.mp3 по narration.json (голос ru-RU-SvetlanaNeural)
cd ../src && python3 build-video.py                 # ../demo.mp4 и ../demo.srt (нужен ffmpeg с libass)
```

Озвучку без Python можно сделать в Docker из корня репозитория:
`docker run --rm -v "${PWD}/presentation/voice:/work" -w /work python:3.12-slim sh -c "pip install -q edge-tts && python tts.py"`.

Текст озвучки и субтитров — `voice/narration.json`. Данные в кадре синтетические.
