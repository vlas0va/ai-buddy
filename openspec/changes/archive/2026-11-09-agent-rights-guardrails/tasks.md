## 1. Refuse other people data and actions

- [x] 1.1 RG-01 Colleague absence — test `test_RG_01_*`
- [x] 1.2 RG-02 Named person accesses — test `test_RG_02_*`
- [x] 1.3 RG-20 Mentor sees only own mentees — test `test_RG_20_*`
- [x] 1.4 RG-17 Lead decides own team requests — test `test_RG_17_*`
- [x] 1.5 RG-18 Lead of another team — test `test_RG_18_*`

## 2. Sensitive data and out-of-scope operations

- [x] 2.1 RG-03 Salary — test `test_RG_03_*`
- [x] 2.2 RG-04 Salary of interns — test `test_RG_04_*`
- [x] 2.3 RG-05 Medical data — test `test_RG_05_*`
- [x] 2.4 RG-06 Hiring and dismissal — test `test_RG_06_*`
- [x] 2.5 RG-07 Business trip operation — test `test_RG_07_*`
- [x] 2.6 RG-11 Benefit operation — test `test_RG_11_*`
- [x] 2.7 RG-12 Personal documents — test `test_RG_12_*`
- [x] 2.8 RG-13 Redirect to HR BP — test `test_RG_13_*`

## 3. Prompt injection resistance

- [x] 3.1 RG-08 Ignore instructions — test `test_RG_08_*`
- [x] 3.2 RG-09 Tool name in a message — test `test_RG_09_*`
- [x] 3.3 RG-10 Role play — test `test_RG_10_*`
- [x] 3.4 RG-19 Injection inside a document — test `test_RG_19_*`

## 4. Tools by role and write guard

- [x] 4.1 RG-14 Intern asks for lead tools — test `test_RG_14_*`
- [x] 4.2 RG-15 Read tools per role — test `test_RG_15_*`
- [x] 4.3 RG-16 Gateway blocks writes — test `test_RG_16_*`

## 5. Mock services enforce rights themselves

- [x] 5.1 MCP-01 Identity only from JWT — test `test_MCP_01_*`
- [x] 5.2 MCP-02 Foreign or missing confirmation — test `test_MCP_02_*`
- [x] 5.3 MCP-03 Idempotent write — test `test_MCP_03_*`
- [x] 5.4 MCP-04 Own records only — test `test_MCP_04_*`
- [x] 5.5 MCP-05 Lead of own team only — test `test_MCP_05_*`
- [x] 5.6 MCP-06 Elevated access creates an approval — test `test_MCP_06_*`
- [x] 5.7 MCP-07 KB search is ACL-filtered — test `test_MCP_07_*`
- [x] 5.8 MCP-08 Directory and checklist — test `test_MCP_08_*`
- [x] 5.9 MCP-09 Fault injection and adapters — test `test_MCP_09_*`
- [x] 5.10 MCP-10 Validation problems — test `test_MCP_10_*`
- [x] 5.11 CT-01 Canonical args hash — test `test_CT_01_*`
- [x] 5.12 CT-02 Token verification matrix — test `test_CT_02_*`
- [x] 5.13 CT-03 Malformed tokens — test `test_CT_03_*`

## 6. Verification

- [x] 6.1 `pytest --cov` > 80% and `make eval` green on stub
