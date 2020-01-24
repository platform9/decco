# Springboard

Springboard is a process manager whose only job is to
- spawn a specified child program
- listen on a TCP port. As soon as anyone connects to that port, terminate the child, then exit

The main use case is currently to act as a process manager for stunnel sidecar containers used inside of Kubernetes job pods.
When the pod's main process completes, it needs a way to terminate the sidecars to cause the overall pod to complete.
It connects to the springboard-equipped sidecars' listening ports to terminate them.
