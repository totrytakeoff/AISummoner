#pragma once

#include <QString>

namespace aisummoner {

// Returns an empty string only when the current process has the platform's
// ordinary-user privilege profile required by Remote.
QString privilegeViolation();

} // namespace aisummoner
