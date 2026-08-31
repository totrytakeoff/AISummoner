#include "daemonlauncher.h"

#include <QCoreApplication>
#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QProcess>
#include <QSaveFile>
#include <QTemporaryDir>
#include <QThread>

#define NOMINMAX
#include <windows.h>
#include <tlhelp32.h>

using namespace aisummoner;

namespace {

constexpr auto factsFileName = "lifecycle-facts.json";
constexpr auto stopFileName = "lifecycle-stop";

QString nativeError(const QString& message) {
    return QStringLiteral("%1 (Windows error %2)")
            .arg(message)
            .arg(static_cast<qulonglong>(GetLastError()));
}

DWORD parentProcessId() {
    const DWORD current = GetCurrentProcessId();
    HANDLE snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snapshot == INVALID_HANDLE_VALUE) return 0;
    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    DWORD parent = 0;
    if (Process32FirstW(snapshot, &entry)) {
        do {
            if (entry.th32ProcessID == current) {
                parent = entry.th32ParentProcessID;
                break;
            }
        } while (Process32NextW(snapshot, &entry));
    }
    CloseHandle(snapshot);
    return parent;
}

bool writeFacts(const QString& dataDirectory, const QStringList& arguments, QString* error) {
    if (!QDir().mkpath(dataDirectory)) {
        *error = QStringLiteral("cannot create lifecycle data directory");
        return false;
    }
    DWORD sessionId = 0;
    if (!ProcessIdToSessionId(GetCurrentProcessId(), &sessionId)) {
        *error = nativeError(QStringLiteral("cannot read lifecycle session"));
        return false;
    }
    QJsonArray encodedArguments;
    for (const QString& argument : arguments) encodedArguments.append(argument);
    const QJsonObject facts{
            {QStringLiteral("arguments"), encodedArguments},
            {QStringLiteral("console_window"), GetConsoleWindow() != nullptr},
            {QStringLiteral("current_directory"), QDir::cleanPath(QDir::currentPath())},
            {QStringLiteral("parent_pid"), static_cast<qint64>(parentProcessId())},
            {QStringLiteral("pid"), static_cast<qint64>(GetCurrentProcessId())},
            {QStringLiteral("session_id"), static_cast<qint64>(sessionId)},
    };
    QSaveFile output(QDir(dataDirectory).filePath(QString::fromLatin1(factsFileName)));
    if (!output.open(QIODevice::WriteOnly) ||
        output.write(QJsonDocument(facts).toJson(QJsonDocument::Compact)) < 0 || !output.commit()) {
        *error = QStringLiteral("cannot commit lifecycle facts");
        return false;
    }
    return true;
}

int daemonMode(const QStringList& arguments) {
    if (arguments.size() != 7 || arguments.at(0) != QStringLiteral("daemon") ||
        arguments.at(1) != QStringLiteral("--server") ||
        arguments.at(3) != QStringLiteral("--data-dir") ||
        arguments.at(5) != QStringLiteral("--name")) {
        return 21;
    }
    const QString dataDirectory = QDir::cleanPath(arguments.at(4));
    QString error;
    if (!writeFacts(dataDirectory, arguments, &error)) return 22;
    const QString stopPath = QDir(dataDirectory).filePath(QString::fromLatin1(stopFileName));
    QElapsedTimer timer;
    timer.start();
    while (timer.elapsed() < 30000) {
        if (QFileInfo::exists(stopPath)) return 0;
        QThread::msleep(25);
    }
    return 23;
}

int launcherMode(const QStringList& arguments) {
    if (arguments.size() != 3) return 31;
    DaemonLauncher launcher(QDir::cleanPath(arguments.at(1)), QDir::cleanPath(arguments.at(2)));
    launcher.startDaemon(QStringLiteral("https://127.0.0.1:1"), QStringLiteral("lifecycle-probe"));
    return launcher.isBusy() ? 0 : 32;
}

bool processIsActive(HANDLE process) {
    DWORD exitCode = 0;
    return GetExitCodeProcess(process, &exitCode) && exitCode == STILL_ACTIVE;
}

bool samePath(const QString& left, const QString& right) {
    return QDir::cleanPath(left).compare(QDir::cleanPath(right), Qt::CaseInsensitive) == 0;
}

int coordinatorMode() {
    QTemporaryDir temporary(QDir::tempPath() + QStringLiteral("/aisummoner-lifecycle-XXXXXX"));
    if (!temporary.isValid()) return 41;
    const QString applicationDirectory = temporary.path();
    const QString dataDirectory = QDir(applicationDirectory).filePath(QStringLiteral("data"));
    const QString daemonPath =
            QDir(applicationDirectory).filePath(QStringLiteral("aisummoner-client.exe"));
    if (!QFile::copy(QCoreApplication::applicationFilePath(), daemonPath)) return 42;

    QProcess launcher;
    launcher.setProgram(QCoreApplication::applicationFilePath());
    launcher.setArguments({QStringLiteral("--launcher"), applicationDirectory, dataDirectory});
    launcher.setWorkingDirectory(QDir::homePath());
    launcher.start();
    if (!launcher.waitForStarted(10000)) return 43;
    const qint64 launcherPid = launcher.processId();
    if (!launcher.waitForFinished(10000) || launcher.exitStatus() != QProcess::NormalExit ||
        launcher.exitCode() != 0) {
        return 44;
    }

    const QString factsPath = QDir(dataDirectory).filePath(QString::fromLatin1(factsFileName));
    QElapsedTimer factsTimer;
    factsTimer.start();
    while (!QFileInfo::exists(factsPath) && factsTimer.elapsed() < 10000) QThread::msleep(25);
    QFile factsFile(factsPath);
    if (!factsFile.open(QIODevice::ReadOnly)) return 45;
    QJsonParseError parseError;
    const QJsonDocument document = QJsonDocument::fromJson(factsFile.readAll(), &parseError);
    if (parseError.error != QJsonParseError::NoError || !document.isObject()) return 46;
    const QJsonObject facts = document.object();
    const DWORD childPid = static_cast<DWORD>(facts.value(QStringLiteral("pid")).toInteger());
    HANDLE child = OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_TERMINATE,
                               FALSE, childPid);
    if (!child) return 47;
    struct ProcessCloser {
        HANDLE handle;
        bool stopRequested = false;
        ~ProcessCloser() {
            if (!stopRequested && processIsActive(handle)) TerminateProcess(handle, 99);
            CloseHandle(handle);
        }
    } childCloser{child};

    const QJsonArray actualArguments = facts.value(QStringLiteral("arguments")).toArray();
    const QJsonArray expectedArguments{
            QStringLiteral("daemon"),
            QStringLiteral("--server"),
            QStringLiteral("https://127.0.0.1:1"),
            QStringLiteral("--data-dir"),
            QDir::cleanPath(dataDirectory),
            QStringLiteral("--name"),
            QStringLiteral("lifecycle-probe"),
    };
    if (facts.value(QStringLiteral("console_window")).toBool(true) ||
        facts.value(QStringLiteral("parent_pid")).toInteger() != launcherPid ||
        actualArguments != expectedArguments ||
        !samePath(facts.value(QStringLiteral("current_directory")).toString(), QDir::homePath()) ||
        !processIsActive(child)) {
        return 48;
    }

    QFile stop(QDir(dataDirectory).filePath(QString::fromLatin1(stopFileName)));
    if (!stop.open(QIODevice::WriteOnly) || stop.write("stop\n") < 0) return 49;
    stop.close();
    childCloser.stopRequested = true;
    if (WaitForSingleObject(child, 10000) != WAIT_OBJECT_0) {
        childCloser.stopRequested = false;
        return 50;
    }
    DWORD exitCode = 0;
    if (!GetExitCodeProcess(child, &exitCode) || exitCode != 0) return 51;
    return 0;
}

}  // namespace

int main(int argc, char** argv) {
    QCoreApplication application(argc, argv);
    const QStringList arguments = application.arguments().mid(1);
    if (!arguments.isEmpty() && arguments.first() == QStringLiteral("daemon")) {
        return daemonMode(arguments);
    }
    if (!arguments.isEmpty() && arguments.first() == QStringLiteral("--launcher")) {
        return launcherMode(arguments);
    }
    if (!arguments.isEmpty()) return 2;
    return coordinatorMode();
}
