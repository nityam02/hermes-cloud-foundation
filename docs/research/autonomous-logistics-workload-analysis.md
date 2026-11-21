# Workload analysis: autonomous drone logistics

Status: **research note, not a specification.** This note describes the workload of *a
representative autonomous drone logistics operator*: a company flying beyond-visual-line-of-sight
(BVLOS) delivery drones for consumer, partner-platform and healthcare customers in the EU/EEA. It
feeds [the reference architecture](../architecture/autonomous-logistics-reference-architecture.md).
It describes no specific company. Every number marked **ASSUMPTION** is an engineering estimate
with a range, not a measurement.

Evidence labels used below:

- **verified**: stated in a cited regulator, standard or provider document.
- **inferred**: reasoned from public industry information; the reasoning is given.
- **assumption**: an estimate to be replaced by measurements.

---

## 1. Operational workflows

| # | Workflow | Actors | Notes |
|---|---|---|---|
| W1 | **Order intake**: consumer app, partner-platform API or webhook, or a scheduled healthcare route | customer, partner system, hospital logistics staff | Three sources with different SLAs. Healthcare routes are often recurring and booked ahead. Consumer and partner orders are on demand with meal-time peaks (inferred). |
| W2 | **Eligibility and quote**: is the address inside a service area, is there a feasible drop point, is it outside restricted geozones? | backend | A geospatial query on every order and every address sign-up. |
| W3 | **Mission planning**: route, altitude profile, energy and battery estimate, airspace checks (geozones, NOTAMs or danger areas, U-space authorisation where applicable), weather go/no-go | scheduler, remote pilot / OCC | Must be reproducible and recorded, because it is evidence for the operational authorisation. |
| W4 | **Readiness and crew assignment**: aircraft serviceable (maintenance status), battery state, remote pilot on shift, base crew available | base crew, OCC, maintenance | Driven by human rosters. Must work with paper or offline tools. |
| W5 | **Loading and pre-flight**: payload loaded and sealed, pre-flight checklist done, mission uploaded to the aircraft | base crew | Chain-of-custody handover point for healthcare payloads. |
| W6 | **Launch and cruise**: autonomous flight under onboard autonomy with supervision from an Operational Control Centre (OCC), possibly one-to-many | onboard autopilot, remote pilot | Safety-critical. Runs onboard or at the OCC, never in the cloud (see the reference architecture's safety boundary). |
| W7 | **Delivery**: hover and lower the payload on a tether/winch, or land, at a validated drop point; release; proof of delivery | onboard, customer | Proof of delivery may include an image or a release-sensor event. |
| W8 | **Return and turnaround**: landing, battery swap or charge, inspection, next mission | base crew | Determines fleet utilisation. |
| W9 | **Post-flight**: upload flight logs, close the mission record, reconcile telemetry gaps | edge node, backend | Logs may arrive hours later (store-and-forward). |
| W10 | **Occurrence and safety reporting**: unusual technical or operational events, external safety reports | remote pilot, operations manager | Regulatory record (UAS.SPEC.050). Some events also need reporting to the authority. |
| W11 | **Maintenance and airworthiness**: scheduled and unscheduled tasks, component life, firmware versions | technicians | Feeds W4 readiness. Regulatory record. |
| W12 | **Training and competence**: remote-pilot and crew qualifications and currency | operations manager | Regulatory record. Kept after personnel leave (see §5.2). |

## 2. Components and their infrastructure needs

| Component | Primary needs | Hermes v1alpha1 fit |
|---|---|---|
| Customer app backend / public API | HTTPS ingress, auth, autoscaling for meal-time peaks, rate limiting | `compute` (public service) |
| Partner integration gateway | Inbound webhooks, outbound callbacks, per-partner secrets, a fixed egress IP for allow-listing (often needed by partners) | `compute`, `secrets`, `network` (NAT egress) |
| Order management | Transactions, idempotency keys, status history | `database` (PostgreSQL) |
| Delivery orchestration | State machine per order and mission, durable events, retries, dead-letter | `compute`, `messaging`, `database` |
| Mission scheduling | CPU bursts for planning, constraint solving (bases, crew, aircraft, weather, airspace) | `compute`, `database` |
| Geospatial service | Polygons and line-strings, indexes, intersection and distance queries | `database` with PostGIS |
| Fleet management | Aircraft registry, maintenance, firmware, battery cycles, readiness | `database`, `storage` |
| Telemetry pipeline | Ingest of out-of-order, late and duplicated position/health data; hot store; archive | `messaging` + `compute` + `database`/`storage` (device-facing broker out of scope, see the reference architecture) |
| Notifications | Push, SMS and e-mail fan-out, customer ETA updates | `messaging`, `compute` |
| Operational dashboard (OCC view) | Real-time push to browsers (WebSocket or SSE), map layers, alerting | `compute`, `observability` |
| Records archive | Multi-year, tamper-evident, access-controlled storage | `storage` (versioning; immutability locks are out of scope for v1alpha1) |
| External adapters | Traffic-awareness feeds, network remote ID, U-space services, weather, maps, NOTAM data | `compute`, `secrets`, `messaging` |

## 3. Data characteristics (ASSUMPTIONS)

All values below are **assumptions** for capacity planning. Replace them with measurements.

### 3.1 Per-flight data

| Data | Rate / size | Per flight (10–40 min) |
|---|---|---|
| Cloud-forwarded telemetry (position, attitude, speed, battery, link quality, mode) | 1–10 Hz; 200–1,000 bytes per message | about 0.1–24 MB |
| Full-rate onboard flight log (autopilot log) | uploaded after the flight | 5–200 MB |
| Imagery (proof of delivery, inspection) | 1–10 images at 0.2–5 MB | 0.2–50 MB |
| Live video (only if streamed) | 0.5–4 Mbit/s | about 40 MB–1.2 GB; usually kept off the cloud path or sampled |
| Mission and plan records, events | tens to hundreds of events at under 4 KB | under 1 MB |

### 3.2 Fleet-level throughput

Concurrent airborne drones are estimated as flights/day × average flight hours ÷ operating
hours/day.

| Tier (ASSUMPTION) | Flights/day | Concurrent airborne | Telemetry msgs/s (1–10 Hz) | Telemetry/day | Flight logs/day |
|---|---|---|---|---|---|
| Small (1–2 bases) | 10–100 | 1–5 | 1–50 | 1 MB–2.4 GB | 50 MB–20 GB |
| Medium (3–10 bases) | 100–1,000 | 5–40 | 5–400 | 10 MB–24 GB | 0.5–200 GB |
| Enterprise (10+ bases, multi-country) | 1,000–10,000 | 40–350 | 40–3,500 | 0.1–240 GB | 5 GB–2 TB |

Inferred consequences:

- Event volume is **modest** at every tier. Even the enterprise upper bound (about 3,500 msg/s)
  fits managed pub/sub services and a partitioned PostgreSQL table if messages are batched (for
  example one message per drone per second carrying 1–10 samples).
- **Flight logs and imagery dominate storage.** Object storage lifecycle rules and multi-year
  retention dominate cost, not compute.
- Load is **bursty**: meal-time peaks and weather windows. Scale-to-zero suits only non-critical
  services. Ingest and dispatch should keep a warm minimum.

### 3.3 Retention (ASSUMPTION, to be confirmed against the operational authorisation)

| Data | Hot | Archive |
|---|---|---|
| Raw telemetry | 7–90 days (queryable) | Downsampled or per-flight summary kept with the flight record |
| Flight records, occurrences, maintenance, training | lifetime of the operation in the database | Multi-year (see §5.2) in the immutable archive |
| Flight logs, imagery | 30–180 days in standard storage | Multi-year in cold storage, or deleted per policy and GDPR minimisation |
| Personal data (customer addresses, contact) | as long as the account is active | Deleted or anonymised per GDPR storage limitation; keep operational records pseudonymous |

## 4. Latency and availability

| Path | Latency budget (ASSUMPTION) | Availability posture |
|---|---|---|
| Onboard control loops, geofence enforcement, lost-link and emergency behaviour | milliseconds, **onboard only** | Must not depend on any network or cloud |
| Remote pilot command and control (C2) link | about 100 ms–1 s over cellular | Uses a dedicated, monitored link with an independent fallback. It is not a cloud managed service dependency. |
| OCC situational display (fleet positions, traffic) | 1–3 s freshness | Degrades to an independent ground-station view |
| Dispatch decision (new mission release) | 1–10 s | **Fail-closed**: no dispatch if state cannot be confirmed |
| Order API (customer and partner) | p95 < 500 ms | 99.5–99.9% (ASSUMPTION). Orders can queue during short outages. |
| Customer ETA and notifications | 5–30 s | Best effort |
| Records archive | minutes | High durability matters more than availability |

## 5. Regulatory-driven requirements

This section is engineering interpretation, **not legal advice**. Confirm with the competent
authority and the operational authorisation.

### 5.1 Specific category, SORA 2.5

- BVLOS delivery operations generally fall in the **specific** category of Regulation (EU)
  2019/947. They need an operational authorisation based on a risk assessment (verified:
  Regulation (EU) 2019/947, Article 11 and Annex Part B).
- EASA **ED Decision 2025/018/R** (29 September 2025) updates the AMC/GM to Regulation (EU)
  2019/947 (Issue 1, Amendment 4) and introduces the **SORA 2.5** package (verified: EASA ED
  Decision page). The page states no applicability date, so check transition arrangements with
  the national authority.
- The SAIL that results from the ground and air risk sets how robust the **operational safety
  objectives (OSOs)** must be (procedures, training, maintenance, C2 link performance, containment,
  emergency response). Software implications (inferred):
  - versioned, reviewable procedures and configuration (change management is evidence)
  - recorded mission plans and pre-flight checks
  - monitored C2 link performance with alerts
  - an emergency response plan that does not assume cloud availability
  - security measures against unlawful interference and unauthorised access (verified as an
    operator responsibility: EASA FAQ on specific-category responsibilities)

### 5.2 Record-keeping (UAS.SPEC.050)

- The UAS operator must keep records of UAS operations, as the authorisation requires (verified:
  EASA FAQ citing UAS.SPEC.050 of Regulation (EU) 2019/947).
- The UK CAA's AMC1 UAS.SPEC.050(1)(g) and the UK-retained text of 2019/947 say to keep
  records of personnel qualifications and of operations, including unusual technical or
  operational occurrences, for **at least 3 years**. Records must be protected from unauthorised
  access, damage, alteration and theft (verified for the UK text and AMC).
- **Caveat:** I did not confirm the 3-year figure in the current EU consolidated text of
  2019/947. Treat "3 years" as a probable minimum and verify on EUR-Lex and with the national
  authority. The authorisation may require longer.
- Implications:
  - an append-only, tamper-evident archive (object storage with versioning plus a retention lock
    or WORM, hash-chained manifests)
  - access logging
  - restore tests
  - personnel qualification records retained after the person leaves

  Database point-in-time recovery (≤35 days in Hermes) is **not** a records archive.

### 5.3 U-space

- Regulation (EU) 2021/664 defines U-space airspace and services, provided by certified U-space
  service providers (USSPs) and coordinated through a common information service (CIS). Services
  include network identification, geo-awareness, UAS flight authorisation, traffic information,
  weather and conformance monitoring (verified: Regulation (EU) 2021/664).
- Implications (inferred):
  - USSP and CIS integrations are **external, replaceable adapters**
  - flight-authorisation requests and responses are records
  - national rollout status varies and can change, so keep the integration behind
    configuration
- Outside designated U-space airspace, operators use other airspace mitigations (for example
  NOTAMs, danger areas or temporary segregation, depending on the country and the authorisation).
  The mission planner needs per-jurisdiction rules.

### 5.4 Remote ID and network ID

- **Direct remote identification** (broadcast from the aircraft) is defined for class-marked UAS
  and add-on modules in Delegated Regulation (EU) 2019/945. Operators must upload their
  registration number into the remote-ID system (verified: EASA FAQ).
- **Network remote identification** is a U-space service (Regulation (EU) 2021/664). ASTM F3411
  standardises broadcast and network remote ID (verified: standard exists).
- Implications (inferred):
  - publishing positions to a network-ID or traffic-awareness service is an outbound integration
    with its own outage behaviour
  - broadcast remote ID is onboard and must keep working when the cloud is down

### 5.5 Geographical zones and geo-awareness

- Operators must keep geo-awareness data up to date (verified: EASA FAQ).
- Implications: geozone data is versioned in the database, and the version is recorded per
  mission. The aircraft holds its own copy, so onboard geofencing works offline.

## 6. Intermittent connectivity

- **Expect** cellular (LTE/5G) loss: coverage holes at altitude, cell handover on long routes,
  carrier outages and congestion. Mitigations (inferred industry practice):
  - multi-carrier or dual-modem links
  - an **independent ground-station network** (separate radio receivers for traffic awareness and
    aircraft position) that keeps working when the carrier or the cloud fails
  - predefined onboard lost-link behaviour (continue, hold, return or land per the authorisation)
- **Cloud ingest requirements:**
  - Each telemetry message carries `aircraft_id`, `mission_id`, a monotonic `seq` and the
    device-side `ts`.
  - Ingest is **idempotent** (deduplicate on `aircraft_id`+`seq`) and accepts **out-of-order and
    late** data, hours late for store-and-forward logs.
  - Freshness is computed from device time and surfaced per aircraft ("last seen N s ago").
  - Gaps are recorded explicitly, not interpolated silently.

## 7. Edge operations (per base)

A base runs safely with no cloud connection (inferred requirement). A small **edge node** per
base:

- caches the active mission set, geozones, aircraft readiness and crew roster
- runs or hosts the ground control station software and the local operator view
- buffers flight logs and telemetry for later upload (store-and-forward)
- keeps a local, signed copy of procedures and checklists

The edge node may **complete and recover** in-flight missions without the cloud. It must **not
release new missions** unless the authoritative state (order, aircraft readiness, airspace
authorisation) is confirmed and fresh. Edge hardware, OS and VPN are outside Hermes v1alpha1.

## 8. Offline workflows

- Offline-capable crew tools for checklists, loading and handover, with later sync and explicit
  conflict rules (the server wins for order state, the device wins for observed events).
- Documented **manual fallback** procedures in the operations manual: manual mission release
  under OCC authority, paper logs that are transcribed later, and phone escalation trees.
- Customer-facing degradation: accept orders as "pending" or pause intake with a clear message,
  rather than accepting orders that cannot be dispatched.

## 9. Healthcare: chain of custody, auditability, GDPR

- **Chain of custody:**
  - record every handover as an immutable event: who, when, where, seal or container ID, and
    condition
  - record environmental data (for example temperature) if the sender requires it
  - support exception handling (missed handover, diverted mission, payload returned)
- **Data minimisation:** the logistics platform should see **pseudonymous sample or consignment
  IDs only**, never patient identity. The hospital system holds the mapping.
- **GDPR** (Regulation (EU) 2016/679):
  - if health data is processed it is special-category data (Art. 9)
  - apply security of processing (Art. 32), a DPIA where required (Art. 35), processor
    agreements with each health organisation (Art. 28), and storage limitation (Art. 5(1)(e))
  - **Tension:** aviation records may need multi-year retention while personal data should be
    minimised. Keep regulatory records pseudonymous so retention duties do not extend personal
    data retention.
- **Auditability:**
  - append-only audit log of user and system actions on missions and consignments
  - customer-managed keys in protected environments (Hermes `security.customer_managed_keys`)
  - EU region selection (`cloud.region`) and documented data residency
- **Multi-tenancy:** several health organisations means per-tenant access control and
  per-tenant reporting.

## 10. Evidence quality

- **Source of this analysis:**
  - public industry information about BVLOS drone delivery (trade press, regulator publications
    and public job descriptions in the sector)
  - EU, EASA and UK CAA regulatory texts and guidance
  - cloud provider and open-source documentation
- **No company identifiers.** This note describes no specific company, customer or partner.
- **Uncertainty:**
  - all rates, volumes and latency budgets are **assumptions**
  - regulatory interpretations are **inferred** engineering consequences, not legal advice
  - the 3-year record-retention figure is **verified only in UK CAA guidance and UK-retained
    text**
  - U-space national rollout status is **not determinable** generically and changes over time
- **Not assumed:** this note assumes no particular cloud provider, message broker, time-series
  database or onboard stack for the representative operator.

## References

- Regulation (EU) 2019/947 (UAS operations): https://eur-lex.europa.eu/eli/reg_impl/2019/947/oj
- Delegated Regulation (EU) 2019/945 (UAS, including direct remote ID): https://eur-lex.europa.eu/eli/reg_del/2019/945/oj
- Regulation (EU) 2021/664 (U-space): https://eur-lex.europa.eu/eli/reg_impl/2021/664/oj
- EASA ED Decision 2025/018/R (AMC/GM update, SORA 2.5): https://www.easa.europa.eu/en/document-library/agency-decisions/ed-decision-2025018r
- EASA FAQ, operator responsibilities in the specific category: https://www.easa.europa.eu/en/faq/119232
- UK CAA AMC1 UAS.SPEC.050(1)(g): https://regulatorylibrary.caa.co.uk/2019-947/Content/AMC-GM/AMC1%20UAS%20SPEC%20050%201%20g%20Responsibilities.htm
- UK-retained text of 2019/947, UAS.SPEC.050: https://legislation.gov.uk/eur/2019/947/annex/part/B/division/5?view=plain
- ASTM F3411 (remote ID and tracking): https://www.astm.org/f3411-22a.html
- GDPR, Regulation (EU) 2016/679: https://eur-lex.europa.eu/eli/reg/2016/679/oj
- MAVLink protocol (common drone telemetry and command protocol): https://mavlink.io/en/
- MQTT 5.0 (OASIS): https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html
- OpenTelemetry OTLP: https://opentelemetry.io/docs/specs/otlp/
- CloudEvents: https://cloudevents.io/
