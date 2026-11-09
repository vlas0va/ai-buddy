## 1. Trainable intent router

- [x] 1.1 NLU-01 Train, save, load — test `test_NLU_01_*`
- [x] 1.2 NLU-02 Training script and quality — test `test_NLU_02_*`
- [x] 1.3 NLU-03 Cross-validation — test `test_NLU_03_*`
- [x] 1.4 NLU-04 No model — test `test_NLU_04_*`

## 2. OpenAI-compatible provider

- [x] 2.1 LLM-01 Stream and complete — test `test_LLM_01_*`
- [x] 2.2 LLM-02 HTTP errors — test `test_LLM_02_*`
- [x] 2.3 LLM-03 Timeouts — test `test_LLM_03_*`
- [x] 2.4 LLM-04 Provider selection — test `test_LLM_04_*`
- [x] 2.5 LLM-05 Embeddings — test `test_LLM_05_*`

## 3. MCP client

- [x] 3.1 MCP-11 Handshake and results — test `test_MCP_11_*`
- [x] 3.2 MCP-12 Tool errors — test `test_MCP_12_*`
- [x] 3.3 MCP-13 HTTP failures — test `test_MCP_13_*`
- [x] 3.4 MCP-14 HTTP backend in the agent — test `test_MCP_14_*`

## 4. Verification

- [x] 4.1 `pytest --cov` > 80% and `make eval` green on stub
