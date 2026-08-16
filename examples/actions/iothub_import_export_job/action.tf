# Registry export with the hub's managed identity, keys excluded:
#   terraform apply -invoke=action.iothub_import_export_job.export
action "iothub_import_export_job" "export" {
  config {
    type                        = "export"
    output_blob_container_uri   = "https://${azurerm_storage_account.backup.name}.blob.core.windows.net/${azurerm_storage_container.registry.name}"
    storage_authentication_type = "identityBased"
    exclude_keys_in_export      = true
    include_configurations      = true
    output_blob_name            = "devices-${formatdate("YYYY-MM-DD", plantimestamp())}.txt"
  }
}

# Import with a container SAS (the container URL followed by the SAS query string):
#   terraform apply -invoke=action.iothub_import_export_job.import
locals {
  migration_container_sas_uri = "${azurerm_storage_account.migration.primary_blob_endpoint}${azurerm_storage_container.migration.name}${data.azurerm_storage_account_blob_container_sas.migration.sas}"
}

action "iothub_import_export_job" "import" {
  config {
    type                      = "import"
    input_blob_container_uri  = local.migration_container_sas_uri
    output_blob_container_uri = local.migration_container_sas_uri
    input_blob_name           = "devices.txt"
    timeout                   = "2h"
  }
}
