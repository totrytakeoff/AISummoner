#pragma once

#include <QByteArray>
#include <QString>

namespace aisummoner {

// Qt's JSON parser keeps the last duplicate object member. IPC v1 rejects
// duplicates before parsing so an attacker cannot create ambiguous envelopes.
bool rejectDuplicateJsonKeys(const QByteArray &json, QString *error);

} // namespace aisummoner
