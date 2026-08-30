#include "platformsecurity.h"

#ifdef Q_OS_WIN

#define NOMINMAX
#include <windows.h>

#include <QByteArray>

namespace {

bool queryToken(HANDLE token, TOKEN_INFORMATION_CLASS informationClass, QByteArray *buffer)
{
    DWORD required = 0;
    if (GetTokenInformation(token, informationClass, nullptr, 0, &required)
        || GetLastError() != ERROR_INSUFFICIENT_BUFFER || required == 0) {
        return false;
    }
    buffer->resize(static_cast<qsizetype>(required));
    return GetTokenInformation(token, informationClass, buffer->data(), required, &required);
}

bool isServiceAccount(PSID userSid)
{
    const WELL_KNOWN_SID_TYPE kinds[] = {
        WinLocalSystemSid, WinLocalServiceSid, WinNetworkServiceSid,
    };
    for (const auto kind : kinds) {
        BYTE storage[SECURITY_MAX_SID_SIZE]{};
        DWORD size = sizeof(storage);
        if (!CreateWellKnownSid(kind, nullptr, storage, &size)) return true;
        if (EqualSid(userSid, storage)) return true;
    }
    return false;
}

} // namespace

#else

#include <unistd.h>

#endif

namespace aisummoner {

QString privilegeViolation()
{
#ifdef Q_OS_WIN
    HANDLE token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) {
        return QStringLiteral("无法验证当前 Windows 用户权限，程序已安全停止。");
    }
    struct TokenCloser {
        HANDLE token;
        ~TokenCloser() { CloseHandle(token); }
    } closer{token};

    TOKEN_ELEVATION elevation{};
    DWORD returned = 0;
    if (!GetTokenInformation(token, TokenElevation, &elevation, sizeof(elevation), &returned)) {
        return QStringLiteral("无法验证当前 Windows 用户权限，程序已安全停止。");
    }
    QByteArray integrityStorage;
    QByteArray userStorage;
    if (!queryToken(token, TokenIntegrityLevel, &integrityStorage)
        || !queryToken(token, TokenUser, &userStorage)) {
        return QStringLiteral("无法验证当前 Windows 用户权限，程序已安全停止。");
    }
    const auto *integrity = reinterpret_cast<const TOKEN_MANDATORY_LABEL *>(integrityStorage.constData());
    const auto *user = reinterpret_cast<const TOKEN_USER *>(userStorage.constData());
    if (!IsValidSid(integrity->Label.Sid) || !IsValidSid(user->User.Sid)) {
        return QStringLiteral("无法验证当前 Windows 用户权限，程序已安全停止。");
    }
    const DWORD count = *GetSidSubAuthorityCount(integrity->Label.Sid);
    if (count == 0) {
        return QStringLiteral("无法验证当前 Windows 用户权限，程序已安全停止。");
    }
    const DWORD integrityRid = *GetSidSubAuthority(integrity->Label.Sid, count - 1);
    DWORD session = 0;
    if (!ProcessIdToSessionId(GetCurrentProcessId(), &session)) {
        return QStringLiteral("无法验证当前 Windows 用户权限，程序已安全停止。");
    }
    if (elevation.TokenIsElevated != 0 || integrityRid >= SECURITY_MANDATORY_HIGH_RID
        || session == 0 || isServiceAccount(user->User.Sid)) {
        return QStringLiteral("出于安全考虑，被控客户端必须由普通桌面用户启动，请勿使用管理员身份运行。");
    }
    return {};
#else
    if (geteuid() == 0) {
        return QStringLiteral("出于安全考虑，被控客户端不能以 root 身份运行。");
    }
    return {};
#endif
}

} // namespace aisummoner
