AISummoner Windows Remote engineering bundle
=============================================

This unsigned Task027 bundle validates the production Windows Core identity,
local IPC, pairing/status foundation, non-interactive PowerShell exec and
interactive ConPTY Terminal. It is an engineering artifact, not a supported
release: the hosted CI account is elevated, ordinary-user Windows 11/10 and
clean-VM acceptance remain open, and Authenticode is not present.

SSH non-PTY exec now uses inbox Windows PowerShell 5.1 with suspended Job
assignment, separate UTF-8 stdout/stderr and joined descendant cleanup.
Interactive Terminal now uses the same PowerShell through production ConPTY,
including VT/UTF-8, resize, Ctrl-C and joined Job/pipe/handle cleanup. Windows
Agent use remains unavailable until Task029 adds the Server-owned cwd/Execution
Profile and real DSH proof. Git Bash/MSYS2 is not bundled or required.

Included executables:

- aisummoner-client-ui.exe: the Qt Remote UI Windows build.
- aisummoner-client.exe: the production Remote Core with Windows Runtime,
  DPAPI Device Identity, authenticated named-pipe IPC, PowerShell/Job exec and
  ConPTY Terminal.
- windows-contract-probe.exe: the Go security, IPC and runtime probe server.
- aisummoner_windows_ipc_probe.exe: the Qt-to-Go named-pipe verifier.
- vc_redist.x64.exe: the official Visual C++ Redistributable collected by
  windeployqt. A clean test VM may need to install it before launching the Qt
  executables; a future production installer will own that prerequisite.

Do not substitute or rename the probe executables. A supported Windows package
will be produced only after ordinary-user GUI launch, real public Server/
Controller Terminal/Agent and clean-VM gates have passed their own reviews.
