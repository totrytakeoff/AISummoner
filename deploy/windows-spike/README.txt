AISummoner Windows Remote engineering bundle
=============================================

This unsigned Task025 bundle validates the production Windows Core identity,
local IPC and pairing/status foundation. It is an engineering artifact, not a
supported release: the hosted CI account is elevated, ordinary-user Windows
11/10 and clean-VM acceptance remain open, and Authenticode is not present.

Terminal and Agent execution are intentionally unavailable in this bundle.
The Core rejects SSH exec and shell requests until the PowerShell/Job Object
and ConPTY backends pass Tasks026-027. Git Bash/MSYS2 is not bundled or required.

Included executables:

- aisummoner-client-ui.exe: the Qt Remote UI Windows build.
- aisummoner-client.exe: the production Remote Core with Windows Runtime,
  DPAPI Device Identity and authenticated named-pipe IPC backends.
- windows-contract-probe.exe: the Go security, IPC and runtime probe server.
- aisummoner_windows_ipc_probe.exe: the Qt-to-Go named-pipe verifier.
- vc_redist.x64.exe: the official Visual C++ Redistributable collected by
  windeployqt. A clean test VM may need to install it before launching the Qt
  executables; a future production installer will own that prerequisite.

Do not substitute or rename the probe executables. A supported Windows package
will be produced only after PowerShell exec, ConPTY, ordinary-user GUI launch,
real Tunnel/Terminal/Agent and clean-VM gates have passed their own reviews.
