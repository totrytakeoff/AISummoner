#include "appsettings.h"
#include "daemonclient.h"
#include "daemonlauncher.h"
#include "platformsecurity.h"

#include <QByteArray>
#include <QCoreApplication>
#include <QDir>
#include <QElapsedTimer>
#include <QEventLoop>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonObject>
#include <QProcess>
#include <QSaveFile>
#include <QSet>
#include <QStandardPaths>
#include <QThread>
#include <QTimer>

#include <cstdio>
#include <iterator>
#include <string>

#define NOMINMAX
#include <windows.h>
#include <tlhelp32.h>

using namespace aisummoner;

namespace {

struct NativeProcess {
    DWORD pid = 0;
    HANDLE handle = nullptr;

    ~NativeProcess()
    {
        if (!handle) return;
        DWORD exitCode = 0;
        if (GetExitCodeProcess(handle, &exitCode) && exitCode == STILL_ACTIVE) {
            TerminateProcess(handle, 98);
            WaitForSingleObject(handle, 10000);
        }
        CloseHandle(handle);
    }
};

struct QtProcessGuard {
    QProcess *process;
    ~QtProcessGuard()
    {
        if (process->state() == QProcess::NotRunning) return;
        process->kill();
        process->waitForFinished(10000);
    }
};

QString normalizedPath(const QString &value)
{
    return QDir::toNativeSeparators(QDir::cleanPath(value));
}

QSet<DWORD> matchingProcesses(const QString &executablePath)
{
    QSet<DWORD> result;
    HANDLE snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snapshot == INVALID_HANDLE_VALUE) return result;
    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    if (Process32FirstW(snapshot, &entry)) {
        do {
            HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE,
                                         entry.th32ProcessID);
            if (!process) continue;
            std::wstring path(32768, L'\0');
            DWORD size = static_cast<DWORD>(path.size());
            if (QueryFullProcessImageNameW(process, 0, path.data(), &size)) {
                path.resize(size);
                const QString actual = QString::fromStdWString(path);
                if (normalizedPath(actual).compare(normalizedPath(executablePath),
                                                   Qt::CaseInsensitive) == 0) {
                    result.insert(entry.th32ProcessID);
                }
            }
            CloseHandle(process);
        } while (Process32NextW(snapshot, &entry));
    }
    CloseHandle(snapshot);
    return result;
}

bool waitForNewProcess(const QString &path, const QSet<DWORD> &before,
                       NativeProcess *process)
{
    QElapsedTimer timer;
    timer.start();
    while (timer.elapsed() < 15000) {
        const QSet<DWORD> current = matchingProcesses(path);
        for (const DWORD pid : current) {
            if (before.contains(pid)) continue;
            HANDLE handle = OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION
                                            | PROCESS_TERMINATE,
                                        FALSE, pid);
            if (handle) {
                process->pid = pid;
                process->handle = handle;
                return true;
            }
        }
        QCoreApplication::processEvents(QEventLoop::AllEvents, 25);
        QThread::msleep(25);
    }
    return false;
}

bool processIsActive(HANDLE process)
{
    DWORD exitCode = 0;
    return process && GetExitCodeProcess(process, &exitCode) && exitCode == STILL_ACTIVE;
}

bool waitForStatus(RemoteStatus *status, int timeoutMs = 15000)
{
    DaemonClient client(AppSettings::defaultSocketPath(), nullptr, 1000, 100);
    QEventLoop loop;
    QTimer timeout;
    timeout.setSingleShot(true);
    timeout.setInterval(timeoutMs);
    bool received = false;
    QObject::connect(&timeout, &QTimer::timeout, &loop, &QEventLoop::quit);
    QObject::connect(&client, &DaemonClient::statusChanged, &loop,
                     [&](const RemoteStatus &value) {
        *status = value;
        received = true;
        loop.quit();
    });
    client.start();
    timeout.start();
    loop.exec();
    client.stop();
    return received && client.isAvailable();
}

void diagnoseCoreStartup(const QString &stage, const QString &dataDirectory,
                         NativeProcess *core)
{
    QJsonObject diagnostic{
        {QStringLiteral("core_active"), processIsActive(core->handle)},
        {QStringLiteral("data_directory"), dataDirectory},
        {QStringLiteral("generic_data_directory"),
         QStandardPaths::writableLocation(QStandardPaths::GenericDataLocation)},
        {QStringLiteral("home_directory"), QDir::homePath()},
        {QStringLiteral("local_appdata_environment"),
         QString::fromLocal8Bit(qgetenv("LOCALAPPDATA"))},
        {QStringLiteral("socket_name"), AppSettings::defaultSocketPath()},
        {QStringLiteral("user_profile_environment"),
         QString::fromLocal8Bit(qgetenv("USERPROFILE"))},
    };
    DWORD exitCode = 0;
    if (core->handle && GetExitCodeProcess(core->handle, &exitCode)) {
        diagnostic.insert(QStringLiteral("core_exit_code"), static_cast<qint64>(exitCode));
    }
    if (processIsActive(core->handle)) {
        TerminateProcess(core->handle, 97);
        WaitForSingleObject(core->handle, 10000);
    }

    QDir(dataDirectory).removeRecursively();
    QString validationError;
    const auto spec = DaemonLauncher::buildLaunchSpec(
        stage, dataDirectory, QStringLiteral("https://127.0.0.1:1"),
        QStringLiteral("task028-package-diagnostic"), &validationError);
    diagnostic.insert(QStringLiteral("diagnostic_spec_valid"), spec.has_value());
    if (!spec) {
        diagnostic.insert(QStringLiteral("diagnostic_spec_error"), validationError);
    } else {
        QProcess replay;
        QtProcessGuard replayGuard{&replay};
        replay.setProgram(spec->program);
        replay.setArguments(spec->arguments);
        replay.setWorkingDirectory(spec->workingDirectory);
        replay.setProcessChannelMode(QProcess::MergedChannels);
        replay.start();
        const bool started = replay.waitForStarted(10000);
        diagnostic.insert(QStringLiteral("diagnostic_started"), started);
        if (!started) {
            diagnostic.insert(QStringLiteral("diagnostic_process_error"), replay.errorString());
        } else {
            RemoteStatus ignored;
            diagnostic.insert(QStringLiteral("diagnostic_ipc_available"),
                              waitForStatus(&ignored, 5000));
            diagnostic.insert(QStringLiteral("diagnostic_running"),
                              replay.state() != QProcess::NotRunning);
            if (replay.state() == QProcess::NotRunning) {
                diagnostic.insert(QStringLiteral("diagnostic_exit_code"), replay.exitCode());
            }
        }
        diagnostic.insert(QStringLiteral("diagnostic_output"),
                          QString::fromUtf8(replay.readAll().left(8192)));
    }
    const QByteArray message = QJsonDocument(diagnostic).toJson(QJsonDocument::Compact);
    fwrite(message.constData(), 1, static_cast<size_t>(message.size()), stderr);
    fwrite("\n", 1, 1, stderr);
    fflush(stderr);
}

struct WindowSearch {
    DWORD pid = 0;
    HWND window = nullptr;
};

BOOL CALLBACK findMainWindow(HWND window, LPARAM parameter)
{
    auto *search = reinterpret_cast<WindowSearch *>(parameter);
    DWORD pid = 0;
    GetWindowThreadProcessId(window, &pid);
    if (pid != search->pid || !IsWindowVisible(window) || GetWindow(window, GW_OWNER)) return TRUE;
    wchar_t title[256]{};
    GetWindowTextW(window, title, static_cast<int>(std::size(title)));
    if (QString::fromWCharArray(title) != QStringLiteral("AISummoner Remote")) return TRUE;
    search->window = window;
    return FALSE;
}

HWND waitForMainWindow(DWORD pid)
{
    QElapsedTimer timer;
    timer.start();
    while (timer.elapsed() < 15000) {
        WindowSearch search{pid, nullptr};
        EnumWindows(findMainWindow, reinterpret_cast<LPARAM>(&search));
        if (search.window) return search.window;
        QCoreApplication::processEvents(QEventLoop::AllEvents, 25);
        QThread::msleep(25);
    }
    return nullptr;
}

bool currentTokenFacts(bool *elevated, quint32 *integrityRid, quint32 *sessionId)
{
    HANDLE token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) return false;
    TOKEN_ELEVATION elevation{};
    DWORD returned = 0;
    bool ok = GetTokenInformation(token, TokenElevation, &elevation,
                                  sizeof(elevation), &returned);
    DWORD required = 0;
    GetTokenInformation(token, TokenIntegrityLevel, nullptr, 0, &required);
    QByteArray storage;
    storage.resize(static_cast<qsizetype>(required));
    ok = ok && required > 0
        && GetTokenInformation(token, TokenIntegrityLevel, storage.data(), required, &returned);
    if (ok) {
        const auto *label = reinterpret_cast<const TOKEN_MANDATORY_LABEL *>(storage.constData());
        if (!IsValidSid(label->Label.Sid)) {
            ok = false;
        } else {
            const DWORD count = *GetSidSubAuthorityCount(label->Label.Sid);
            if (count == 0) {
                ok = false;
            } else {
                *integrityRid = *GetSidSubAuthority(label->Label.Sid, count - 1);
            }
        }
    }
    DWORD nativeSession = 0;
    ok = ok && ProcessIdToSessionId(GetCurrentProcessId(), &nativeSession);
    CloseHandle(token);
    *elevated = elevation.TokenIsElevated != 0;
    *sessionId = nativeSession;
    return ok;
}

bool writeProof(const QJsonObject &proof)
{
    const QString directory = QDir(QStandardPaths::writableLocation(
        QStandardPaths::GenericDataLocation)).filePath(QStringLiteral("AISummoner/Task028"));
    if (!QDir().mkpath(directory)) return false;
    QSaveFile output(QDir(directory).filePath(QStringLiteral("package-proof.json")));
    return output.open(QIODevice::WriteOnly)
        && output.write(QJsonDocument(proof).toJson(QJsonDocument::Compact)) >= 0
        && output.commit();
}

int runProbe(const QString &stagePath)
{
    const QFileInfo stageInfo(stagePath);
    const QString stage = stageInfo.canonicalFilePath();
    const QString corePath = QDir(stage).filePath(QStringLiteral("aisummoner-client.exe"));
    const QString guiPath = QDir(stage).filePath(QStringLiteral("aisummoner-client-ui.exe"));
    if (stage.isEmpty() || !QFileInfo::exists(corePath) || !QFileInfo::exists(guiPath)) return 61;
    if (!privilegeViolation().isEmpty()) return 62;

    const QByteArray systemRoot = qgetenv("SystemRoot");
    if (systemRoot.isEmpty()) return 63;
    qputenv("PATH", systemRoot + "\\System32;" + systemRoot);
    qunsetenv("QTDIR");
    qunsetenv("QT_PLUGIN_PATH");
    qunsetenv("QML2_IMPORT_PATH");

    bool elevated = false;
    quint32 integrityRid = 0;
    quint32 sessionId = 0;
    if (!currentTokenFacts(&elevated, &integrityRid, &sessionId)) return 64;

    const QString dataDirectory = AppSettings::defaultDataDirectory();
    QDir(dataDirectory).removeRecursively();
    const QSet<DWORD> before = matchingProcesses(corePath);
    DaemonLauncher launcher(stage, dataDirectory);
    launcher.startDaemon(QStringLiteral("https://127.0.0.1:1"),
                         QStringLiteral("task028-package-probe"));
    if (!launcher.isBusy()) return 65;

    NativeProcess core;
    if (!waitForNewProcess(corePath, before, &core)) return 66;
    RemoteStatus firstStatus;
    if (!waitForStatus(&firstStatus)) {
        diagnoseCoreStartup(stage, dataDirectory, &core);
        return 67;
    }
    launcher.setDaemonAvailable(true);

    QProcess gui;
    QtProcessGuard guiGuard{&gui};
    gui.setProgram(guiPath);
    gui.setWorkingDirectory(stage);
    gui.start();
    if (!gui.waitForStarted(10000)) return 68;
    const HWND mainWindow = waitForMainWindow(static_cast<DWORD>(gui.processId()));
    if (!mainWindow) return 69;
    if (!PostMessageW(mainWindow, WM_CLOSE, 0, 0)) return 70;
    if (!gui.waitForFinished(10000) || gui.exitStatus() != QProcess::NormalExit
        || gui.exitCode() != 0) {
        return 71;
    }
    if (!processIsActive(core.handle)) return 72;
    RemoteStatus afterClose;
    if (!waitForStatus(&afterClose) || afterClose.deviceId != firstStatus.deviceId) return 73;

    const QJsonObject proof{
        {QStringLiteral("core_survived_gui_close"), true},
        {QStringLiteral("elevated"), elevated},
        {QStringLiteral("gui_exit_code"), gui.exitCode()},
        {QStringLiteral("integrity_rid"), static_cast<qint64>(integrityRid)},
        {QStringLiteral("ipc_same_logon"), true},
        {QStringLiteral("platform"), QStringLiteral("windows")},
        {QStringLiteral("session_id"), static_cast<qint64>(sessionId)},
        {QStringLiteral("sanitized_child_environment"), true},
    };
    if (!writeProof(proof)) return 74;
    TerminateProcess(core.handle, 0);
    WaitForSingleObject(core.handle, 10000);
    QDir(dataDirectory).removeRecursively();
    return 0;
}

} // namespace

int main(int argc, char **argv)
{
    QCoreApplication application(argc, argv);
    const QStringList arguments = application.arguments().mid(1);
    if (arguments.size() != 2 || arguments.at(0) != QStringLiteral("--stage")) return 2;
    return runProbe(arguments.at(1));
}
