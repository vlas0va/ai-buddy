## 1. Turn endpoint streams contract events

- [x] 1.1 AG-07 Exactly one terminal event — test `test_AG_07_*`
- [x] 1.2 AG-16 Status stages stay in the contract enum — test `test_AG_16_*`
- [x] 1.3 AG-08 Request id is echoed — test `test_AG_08_*`

## 2. Identity comes from the JWT

- [x] 2.1 AG-02 Missing or invalid token — test `test_AG_02_*`
- [x] 2.2 AG-03 Context mismatch — test `test_AG_03_*`
- [x] 2.3 AG-13 Expired token — test `test_AG_13_*`
- [x] 2.4 AG-15 Unknown role — test `test_AG_15_*`
- [x] 2.5 AG-05 Service token — test `test_AG_05_*`

## 3. Robust input handling and failures

- [x] 3.1 AG-04 Malformed bodies — test `test_AG_04_*`
- [x] 3.2 AG-11 Internal error — test `test_AG_11_*`
- [x] 3.3 AG-12 Tool failure is retryable — test `test_AG_12_*`

## 4. Declarative configuration and operations

- [x] 4.1 AG-01 Health, readiness, metrics — test `test_AG_01_*`
- [x] 4.2 AG-06 Config per role — test `test_AG_06_*`
- [x] 4.3 AG-18 Config validation — test `test_AG_18_*`
- [x] 4.4 AG-09 Clock override for demo — test `test_AG_09_*`
- [x] 4.5 AG-10 FAQ draft from a mentor answer — test `test_AG_10_*`

## 5. Verification

- [x] 5.1 `pytest --cov` > 80% and `make eval` green on stub
