# Disabled Dynamic Filesystem Example

Dynamic NAS `volumeAs: filesystem` provisioning is disabled because its create
workflow cannot be made CSI-idempotent across controller restarts. Use the
`../subpath` example for new dynamic NAS volumes. Existing filesystem PVs
continue to use their legacy mount and delete paths.
