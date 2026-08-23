cmake_minimum_required(VERSION 3.22)

project(AISummonerRemoteClient VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_AUTOMOC ON)
set(CMAKE_AUTOUIC ON)
set(CMAKE_AUTORCC ON)

find_package(Qt6 6.2 REQUIRED COMPONENTS Core Gui Widgets Network)

add_library(aisummoner_remote_ui STATIC
    src/appsettings.cpp
    src/daemonclient.cpp
    src/daemonlauncher.cpp
    src/eventmodel.cpp
    src/mainwindow.cpp
    src/models.cpp
    src/strictjson.cpp
    src/theme.cpp
)
target_include_directories(aisummoner_remote_ui PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(aisummoner_remote_ui PUBLIC
    Qt6::Core
    Qt6::Gui
    Qt6::Widgets
    Qt6::Network
)
target_compile_definitions(aisummoner_remote_ui PUBLIC
    AISUMMONER_GUI_VERSION="${PROJECT_VERSION}"
)
target_compile_options(aisummoner_remote_ui PRIVATE
    $<$<CXX_COMPILER_ID:GNU,Clang>:-Wall;-Wextra;-Wpedantic>
)

qt_add_executable(aisummoner-client-ui src/main.cpp)
target_link_libraries(aisummoner-client-ui PRIVATE aisummoner_remote_ui)
set_target_properties(aisummoner-client-ui PROPERTIES
    OUTPUT_NAME aisummoner-client-ui
)

include(CTest)
if(BUILD_TESTING)
    find_package(Qt6 6.2 REQUIRED COMPONENTS Test)
    qt_add_executable(aisummoner_remote_ui_tests
        tests/test_remoteclientui.cpp
    )
    target_link_libraries(aisummoner_remote_ui_tests PRIVATE
        aisummoner_remote_ui
        Qt6::Test
    )
    add_test(NAME aisummoner_remote_ui_tests COMMAND aisummoner_remote_ui_tests)
    set_tests_properties(aisummoner_remote_ui_tests PROPERTIES
        ENVIRONMENT "QT_QPA_PLATFORM=offscreen"
        TIMEOUT 30
    )
endif()

install(TARGETS aisummoner-client-ui RUNTIME DESTINATION bin)
