AISummoner Windows Remote engineering package
==============================================

This unsigned Task028 package is for controlled testing. It is not a stable
Windows release and may trigger Microsoft Defender SmartScreen. Verify the ZIP
SHA-256 before extracting it. Do not disable antivirus or Windows security
features to run it.

Quick start
-----------

1. Extract the complete AISummoner-Windows-Remote folder to a local NTFS path.
2. If Windows reports that the Visual C++ runtime is missing, install the
   included official vc_redist.x64.exe and restart the UI.
3. Double-click aisummoner-client-ui.exe as your normal desktop user. Do not
   choose "Run as administrator".
4. The UI starts the sibling aisummoner-client.exe Core automatically, connects
   to the built-in AISummoner Server and shows the Device/pairing code.
5. Closing the UI deliberately leaves the Core and outbound connection alive.
   Reopen the UI to inspect it. Use the Pause control when you intend to stop
   remote control sessions.

Current capability
------------------

- Production per-user Core, DPAPI Device Identity and authenticated named pipe.
- Non-interactive Windows PowerShell 5.1 execution with Job cleanup.
- Interactive PowerShell/ConPTY Terminal with UTF-8, resize and Ctrl-C.
- Qt status, pairing, activity, settings and pause/resume UI.

Windows Agent operations are intentionally unavailable until Task029 adds the
Server-owned windows-powershell Execution Profile and a real DSH proof. Git
Bash, MSYS2, WSL and pwsh are not bundled or required.

Support boundary
----------------

The technical floor is Windows 10 1809 x86-64, but the automated native gate is
currently Windows Server 2022. Ordinary-user Windows 11 and Windows 10 22H2
clean-VM acceptance, installer, Authenticode signing, upgrade/uninstall and the
final public Server/Controller E2E remain release gates. The included
package-build.json records the exact source and component hashes.
