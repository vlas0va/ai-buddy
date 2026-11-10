# Презентация и видео

- `AI-buddy.pdf` — продуктовая презентация (14 слайдов, 16:9).
- `demo.mp4` — видео-демо (≈2 мин): S1 → S2 → S3 с согласованием руководителя → S4 → наставничество → права → вид наставника.

Как пересобрать на запущенном стеке (`make up`, web на :5173):

```bash
cd presentation/src
node screenshots.js        # свежие скриншоты в img/ (нужен Playwright)
node topdf.js              # deck.html → ../AI-buddy.pdf 
node record-demo.js        # запись видео в video/*.webm
ffmpeg -i video/*.webm -c:v libx264 -crf 26 -pix_fmt yuv420p -movflags +faststart ../demo.mp4
```

Скрипты рассчитаны на адрес `http://127.0.0.1:5173`; данные в кадре синтетические.
