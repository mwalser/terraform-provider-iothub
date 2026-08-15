# Import ID: <hostname>/twins/<device_id>. The imported resource owns nothing
# yet; the first apply adopts the leaves your configuration declares.
terraform import iothub_device_twin.sensor contoso.azure-devices.net/twins/sensor-0001
