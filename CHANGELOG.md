## 0.1.0 (Unreleased)

Initial development. See CONCEPT.md for the design and roadmap.

FEATURES:

* provider: Entra ID (azidentity chain, `https://iothubs.azure.net/.default`) and shared-access-policy (SAS) authentication; per-construct `hostname` overrides
* **New Resource:** `iothub_device` — identity registry entries with SAS / X.509 authentication, IoT Edge capability, parent/child scopes, write-only keys, import
* **New Resource:** `iothub_module` — module identities with the same authentication options, write-only keys and import
* **New Resource:** `iothub_device_twin`, `iothub_module_twin` — leaf-path ownership of twin tags and desired properties (JSON merge patches; keys written by other systems are never touched); import starts with an empty owned set
* **New Data Source:** `iothub_device`
* **New Data Source:** `iothub_module`
* **New Data Source:** `iothub_modules`
* **New Data Source:** `iothub_device_twin`, `iothub_module_twin` — full twins incl. reported properties
* **New Data Source:** `iothub_statistics`
* **New Ephemeral Resource:** `iothub_device_credentials`
* **New Ephemeral Resource:** `iothub_module_credentials`
