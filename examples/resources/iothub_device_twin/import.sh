# Import ID: <hostname>/twins/<device_id>. The imported resource manages nothing
# yet. The first apply adopts the keys your configuration declares.
terraform import iothub_device_twin.sensor contoso.azure-devices.net/twins/sensor-0001
