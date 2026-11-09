# Changelog

## 1.0.0 (2026-11-09)


### Features

* add checksum-verifying install script with tests ([b63a3cc](https://github.com/drizki/backup-cli/commit/b63a3cc93521e1346f689ae7c99e95c6ca9ac439))
* **backupset:** marker IO and committed-backup scanner ([89e33a9](https://github.com/drizki/backup-cli/commit/89e33a9c56f7f5c5be27ef1f1ba4238eb4c6936b))
* **cli:** implement rm and doctor, replacing the placeholder commands ([d87c6f1](https://github.com/drizki/backup-cli/commit/d87c6f101451f45031c1fe8b9fef7865320675f3))
* **cli:** standard-library flag dispatcher with add, list, job rm and version ([67a4413](https://github.com/drizki/backup-cli/commit/67a4413fe53f505a7bf0d04ddbbddba47fdbc488))
* **cli:** wire run, tail and verify to the engine ([31bd3f8](https://github.com/drizki/backup-cli/commit/31bd3f8cfd02855a3a78c49636e483cb58274ebf))
* **copyx:** explicit-buffer copy with single-pass SHA-256 and metadata apply ([4a65d76](https://github.com/drizki/backup-cli/commit/4a65d76791e44c12e9e8e6b9c89c78ee317aa3b6))
* **doctor:** environment, job and backup checks with injectable system sources ([2e1c47c](https://github.com/drizki/backup-cli/commit/2e1c47c529a9813ec38a085b2c415e905d2bf4e3))
* **engine:** run lifecycle with resume, retry, bitrot recopy, deadline, dry-run and hooks ([0ddee24](https://github.com/drizki/backup-cli/commit/0ddee2415be96f8541d6bdc524c18f50fc642216))
* **engine:** run retention after success and plan dry-run pruning with it ([806065b](https://github.com/drizki/backup-cli/commit/806065b3f6e032c4796306abb08f7c2f2acf07ed))
* **hooks:** shell hooks with environment and timeout ([2dcfd7c](https://github.com/drizki/backup-cli/commit/2dcfd7c20371b50f539a0a8c4aedc7598d2f3986))
* **job:** job model, validation, slugs and deadline parsing ([77d1d9a](https://github.com/drizki/backup-cli/commit/77d1d9a78f7b565872029b5005c586dfddf92724))
* **job:** symlink-safe source/destination overlap check ([0722dcb](https://github.com/drizki/backup-cli/commit/0722dcb655e4d8cd23a9266930693decc7dce9e8))
* **journal:** append-only partial-run journal with torn-line tolerance ([8b9adb4](https://github.com/drizki/backup-cli/commit/8b9adb4243b40c48fcd9a09d2ef5f2666f1dc4bf))
* **lock:** per-job flock ([0bfd613](https://github.com/drizki/backup-cli/commit/0bfd613f01c59c9f2f3163a1b1b66b4e786708d6))
* **manifest:** MANIFEST.sha256 and MANIFEST.tsv writers, parser and sampled verify ([e00c3a0](https://github.com/drizki/backup-cli/commit/e00c3a0781086d005a00a19078d5c2fc7f8e2abe))
* **mount:** mountinfo parsing and mount point lookups ([4072003](https://github.com/drizki/backup-cli/commit/40720031b110f8fcac1eec2326392496ef018b7c))
* **pathguard:** realpath-aware overlap and containment checks ([9c11ed9](https://github.com/drizki/backup-cli/commit/9c11ed95222bfe9fa450b52294b391c31c05aeb8))
* **retention:** prune oldest committed backups through safedelete ([445c409](https://github.com/drizki/backup-cli/commit/445c409f9eb83f90864c310afc1bd99ad6b9a43e))
* **runlog:** timestamped run log mirrored to stdout ([8eb58b2](https://github.com/drizki/backup-cli/commit/8eb58b2bd848bacbfb003b3b60e6ad586d16b3bd))
* **safedelete:** guarded deletion with TOCTOU re-check and repo-wide delete guard test ([b8e19de](https://github.com/drizki/backup-cli/commit/b8e19de8f0caefb09cb842cc89fb62108d306e6e))
* **schedule:** strict schedule parser with OnCalendar, biweekly gate and cron output ([3ad697f](https://github.com/drizki/backup-cli/commit/3ad697f4d458466fd6890226781487d758e8f5ad))
* **space:** free-space estimate and check ([b6248e6](https://github.com/drizki/backup-cli/commit/b6248e6eb2aa900f678a0181ac51113ca7d374f8))
* **state:** atomic JSON state store for jobs and run status ([344900f](https://github.com/drizki/backup-cli/commit/344900f9515b6aec43054ef5f321c4f97489488b))
* **systemd:** unit rendering, Manager interface with real and fake implementations ([3de3332](https://github.com/drizki/backup-cli/commit/3de3332c108401d3944c1570ef1fe61edb398bc5))
* **walk:** metadata-only source walker that never follows symlinks ([54bb6bc](https://github.com/drizki/backup-cli/commit/54bb6bce6c9b271eb80c026f3af41444cd8b414c))


### Bug Fixes

* **build:** use explicit if in release-build argument check ([50a5fd9](https://github.com/drizki/backup-cli/commit/50a5fd910fb36a80b0aa3c60c5d57b6c3d3d5840))
* **ci:** tidy check tolerates a module without go.sum ([2f240a5](https://github.com/drizki/backup-cli/commit/2f240a500ec7edf4079228eac8ca6d3a56c8cd0b))
* **cli:** make rm take the job lock ([fb30689](https://github.com/drizki/backup-cli/commit/fb30689dc339bce52ecdfbe0f11277b3415bc1d0)), closes [#13](https://github.com/drizki/backup-cli/issues/13)
* **engine:** cap the warnings stored in status.json ([e09a9c2](https://github.com/drizki/backup-cli/commit/e09a9c26572b5db436d68042fe50e92a0696380e)), closes [#10](https://github.com/drizki/backup-cli/issues/10)
* **engine:** sweep the partial, tolerate vanished files, trust clean stops ([9ae03d5](https://github.com/drizki/backup-cli/commit/9ae03d56e6cf817b8ada007242c722ffe29b1750)), closes [#2](https://github.com/drizki/backup-cli/issues/2) [#3](https://github.com/drizki/backup-cli/issues/3) [#4](https://github.com/drizki/backup-cli/issues/4) [#6](https://github.com/drizki/backup-cli/issues/6) [#7](https://github.com/drizki/backup-cli/issues/7) [#11](https://github.com/drizki/backup-cli/issues/11)
* **engine:** treat a future-dated marker as old enough for the biweekly gate ([00ae241](https://github.com/drizki/backup-cli/commit/00ae2413d7dfa03857c7a72ae878d6e550b8942f)), closes [#9](https://github.com/drizki/backup-cli/issues/9)
* **job:** clean the destination template on add and load ([bef3c12](https://github.com/drizki/backup-cli/commit/bef3c1272e644daa1bec3179bfc2b253709faeef)), closes [#5](https://github.com/drizki/backup-cli/issues/5)
* **manifest:** escape carriage returns in MANIFEST.sha256 names ([4622565](https://github.com/drizki/backup-cli/commit/46225655c581bf695db8f8b9ca8500573048a160)), closes [#12](https://github.com/drizki/backup-cli/issues/12)
* **safedelete:** remove the marker first and open read-only directories ([767bd2e](https://github.com/drizki/backup-cli/commit/767bd2e8a06d8a0012affcb64441f46f4a36c172)), closes [#8](https://github.com/drizki/backup-cli/issues/8)

## Changelog
