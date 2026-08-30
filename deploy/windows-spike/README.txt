AISummoner Windows Remote compatibility contract
================================================

This unsigned bundle is generated only to validate the Task023 Windows porting
contracts. It is not a production Remote Client and cannot connect a Windows
device to the public AISummoner service yet.

Included executables:

- aisummoner-client-ui.exe: the reusable Qt Remote UI Windows build.
- windows-contract-probe.exe: the Go security, IPC and runtime probe server.
- aisummoner_windows_ipc_probe.exe: the Qt-to-Go named-pipe verifier.

Do not rename windows-contract-probe.exe to aisummoner-client.exe. A usable
Windows package will be produced only after the production Core platform
backends and end-to-end protocol gates have passed their own review.

