#pragma once

#include <QString>
#include <QtGlobal>

namespace aisummoner {

// Pure policy seam shared by the live Windows token query and deterministic
// tests. An empty result means the facts describe an ordinary interactive
// desktop token.
QString windowsPrivilegeViolationForFacts(bool querySucceeded, bool elevated,
                                           quint32 integrityRid, quint32 sessionId,
                                           bool serviceAccount);

// Returns an empty string only when the current process has the platform's
// ordinary-user privilege profile required by Remote.
QString privilegeViolation();

} // namespace aisummoner
