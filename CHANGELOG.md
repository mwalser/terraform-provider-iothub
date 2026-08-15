## 0.1.0 (Unreleased)

Initial release.

FEATURES:

* provider: Microsoft Entra ID (default) and shared-access-policy (SAS) authentication; every construct accepts its own `hostname` (lowercase)
* **New Resource:** `iothub_device` — device identities with SAS / X.509 authentication, IoT Edge capability, parent/child scopes, write-only keys, import
* **New Resource:** `iothub_module` — module identities with the same authentication options, write-only keys and import
* **New Resource:** `iothub_device_twin`, `iothub_module_twin` — manage exactly the twin tags and desired properties you declare; keys written by other systems are never touched, and an imported twin starts without managed values
* **New Resource:** `iothub_configuration` — automatic device/module management configurations; changing the content replaces the configuration
* **New Resource:** `iothub_edge_deployment` — IoT Edge deployments incl. layered deployments; changing `modules_content` replaces the deployment
* **New Data Source:** `iothub_device`, `iothub_module`, `iothub_modules`
* **New Data Source:** `iothub_device_twin`, `iothub_module_twin` — full twins incl. reported properties
* **New Data Source:** `iothub_digital_twin` — the IoT Plug and Play view of a device (document, model ID)
* **New Data Source:** `iothub_configuration`, `iothub_edge_deployment` — incl. the hub's system and custom metric results
* **New Data Source:** `iothub_query` — IoT Hub query language statements, all results
* **New Data Source:** `iothub_statistics`
* **New Data Source:** `iothub_scheduled_job`, `iothub_import_export_job` — job status
* **New Ephemeral Resource:** `iothub_device_credentials`, `iothub_module_credentials` — keys and connection strings that never enter state
* **New Ephemeral Resource:** `iothub_device_sas_token` — short-lived device/module SAS tokens
* **New Action:** `iothub_direct_method` — device/module direct methods with expected status codes
* **New Action:** `iothub_digital_twin_command` — root-level and component Plug and Play commands (SAS authentication only)
* **New Action:** `iothub_scheduled_job` — twin update / device method jobs, waited to completion
* **New Action:** `iothub_import_export_job` — bulk registry export/import (key- or identity-based storage access)
* **New Action:** `iothub_apply_configuration`, `iothub_purge_c2d_queue`, `iothub_cancel_job`
* **New List Resource:** `iothub_device`, `iothub_module`, `iothub_configuration`, `iothub_edge_deployment` — `terraform query` with resource identity (import by identity supported)

NOTES:

* With SAS authentication the hub refuses to create, change or delete modules of a disabled device; the provider reports this with the remedy (enable the device or use Entra ID).
