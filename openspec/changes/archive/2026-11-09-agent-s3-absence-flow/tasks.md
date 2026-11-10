## 1. Explain and collect dates

- [x] 1.1 S3-01 Procedure with source — test `test_S3_01_*`
- [x] 1.2 S3-02 Relative dates — test `test_S3_02_*`
- [x] 1.3 S3-03 Slot filling across turns — test `test_S3_03_*`
- [x] 1.4 S3-17 Unparsed dates — test `test_S3_17_*`

## 2. Day-off request requires explicit confirmation

- [x] 2.1 S3-04 Draft before write — test `test_S3_04_*`
- [x] 2.2 S3-08 Confirmation by text — test `test_S3_08_*`
- [x] 2.3 S3-09 Cancel — test `test_S3_09_*`
- [x] 2.4 S3-18 Proposal shape — test `test_S3_18_*`

## 3. Confirmed request is created, shown and saved

- [x] 3.1 S3-05 Created, shown, saved — test `test_S3_05_*`
- [x] 3.2 S3-06 Remind about the mentor — test `test_S3_06_*`
- [x] 3.3 S3-10 Timeout without duplicates — test `test_S3_10_*`
- [x] 3.4 S3-10b Repeated confirm — test `test_S3_10b_*`
- [x] 3.5 S3-12 Tampered or foreign confirmation — test `test_S3_12_*`

## 4. Validation and scope

- [x] 4.1 S3-07 Weekend — test `test_S3_07_*`
- [x] 4.2 S3-07b Past, too long, overlap — test `test_S3_07b_*`
- [x] 4.3 S3-13 Vacation and sick leave — test `test_S3_13_*`
- [x] 4.4 S3-14 Short notice — test `test_S3_14_*`
- [x] 4.5 S3-15 Scenario flag off — test `test_S3_15_*`
- [x] 4.6 S3-16 Role without absence tools — test `test_S3_16_*`
- [x] 4.7 S3-11 For another person — test `test_S3_11_*`

## 5. Verification

- [x] 5.1 `pytest --cov` > 80% and `make eval` green on stub
