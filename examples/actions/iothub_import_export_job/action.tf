# Nightly registry export into a blob container, keys excluded.
# The hub authenticates to storage with its managed identity
# (Storage Blob Data Contributor on the container); no SAS in the configuration.
action "iothub_import_export_job" "export" {
  config {
    type                        = "export"
    output_blob_container_uri   = "https://${azurerm_storage_account.backup.name}.blob.core.windows.net/${azurerm_storage_container.registry.name}"
    storage_authentication_type = "identityBased"
    exclude_keys_in_export      = true
    include_configurations      = true
    output_blob_name            = "devices-${formatdate("YYYY-MM-DD", timestamp())}.txt"
  }
}

# Bulk import from a devices.txt prepared by a migration pipeline (SAS-based).
# Per-line errors are written to importErrors.log in the output container.
action "iothub_import_export_job" "import" {
  config {
    type                      = "import"
    input_blob_container_uri  = data.azurerm_storage_account_blob_container_sas.migration.sas
    output_blob_container_uri = data.azurerm_storage_account_blob_container_sas.migration.sas
    input_blob_name           = "devices.txt"
    timeout                   = "2h"
  }
}

# Ad hoc:  terraform apply -invoke=action.iothub_import_export_job.export
