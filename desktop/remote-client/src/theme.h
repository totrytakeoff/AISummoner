#pragma once

#include "appsettings.h"

class QApplication;

namespace aisummoner {

bool effectiveDarkTheme(ThemePreference preference);
void applyTheme(QApplication *application, ThemePreference preference);

} // namespace aisummoner
