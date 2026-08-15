## 0.1.0 (Unreleased)

Initial development. See CONCEPT.md for the design and roadmap.

FEATURES:

* provider: Entra ID (azidentity chain, `https://iothubs.azure.net/.default`) and shared-access-policy (SAS) authentication; per-construct `hostname` overrides
* **New Resource:** `iothub_device` — identity registry entries with SAS / X.509 authentication, IoT Edge capability, parent/child scopes, write-only keys, import
* **New Resource:** `iothub_module` — module identities with the same authentication options, write-only keys and import
* **New Resource:** `iothub_device_twin`, `iothub_module_twin` — leaf-path ownership of twin tags and desired properties (JSON merge patches; keys written by other systems are never touched); import starts with an empty owned set
* **New Resource:** `iothub_configuration` — automatic device/module management configurations; content is immutable (replacement), target condition and metric queries are validated against the hub at plan time
* **New Resource:** `iothub_edge_deployment` — IoT Edge deployments incl. layered deployments, same behaviour
* **New Data Source:** `iothub_device`
* **New Data Source:** `iothub_module`
* **New Data Source:** `iothub_modules`
* **New Data Source:** `iothub_device_twin`, `iothub_module_twin` — full twins incl. reported properties
* **New Data Source:** `iothub_configuration`, `iothub_edge_deployment` — incl. the hub's system and custom metric results
* **New Data Source:** `iothub_scheduled_job`, `iothub_import_export_job`
* **New Action:** `iothub_direct_method` — device/module direct methods with expected status codes
* **New Action:** `iothub_scheduled_job` — twin update / device method jobs, waited to completion
* **New Action:** `iothub_import_export_job` — bulk registry export/import (key- or identity-based storage access)
* **New Action:** `iothub_apply_configuration`, `iothub_purge_c2d_queue`, `iothub_cancel_job`
* **New List Resource:** `iothub_device`, `iothub_module`, `iothub_configuration`, `iothub_edge_deployment` — `terraform query` with resource identity (import by identity supported)
* **New Data Source:** `iothub_statistics`
* **New Data Source:** `iothub_query` — IoT Hub query language statements, all pages
* **New Ephemeral Resource:** `iothub_device_credentials`
* **New Ephemeral Resource:** `iothub_module_credentials`
* **New Ephemeral Resource:** `iothub_device_sas_token` — device/module SAS tokens minted locally
