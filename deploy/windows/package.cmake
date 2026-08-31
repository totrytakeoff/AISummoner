cmake_minimum_required(VERSION 3.22)

foreach(name IN ITEMS ROOT_DIR GUI_EXECUTABLE CORE_EXECUTABLE MT_EXECUTABLE
                      DUMPBIN_EXECUTABLE SIGNTOOL_EXECUTABLE WINDEPLOYQT_EXECUTABLE
                      DIST_DIR ARCHIVE_PATH SOURCE_COMMIT
                      PRODUCT_VERSION GO_VERSION QT_VERSION DEFAULT_SERVER_ORIGIN)
    if(NOT DEFINED ${name} OR "${${name}}" STREQUAL "")
        message(FATAL_ERROR "missing required package input: ${name}")
    endif()
endforeach()

file(REAL_PATH "${ROOT_DIR}" root)
file(MAKE_DIRECTORY "${DIST_DIR}")
file(REAL_PATH "${DIST_DIR}" dist)
set(stage "${dist}/AISummoner-Windows-Remote")
string(FIND "${stage}" "${dist}/" stage_prefix)
if(NOT stage_prefix EQUAL 0 OR stage STREQUAL dist)
    message(FATAL_ERROR "unsafe Windows package stage")
endif()

foreach(path IN ITEMS "${GUI_EXECUTABLE}" "${CORE_EXECUTABLE}"
                      "${root}/LICENSE" "${root}/THIRD_PARTY_NOTICES.md"
                      "${root}/deploy/windows/README.txt"
                      "${root}/deploy/windows/QT_SOURCE_OFFER.txt"
                      "${root}/desktop/remote-client/resources/aisummoner-client-ui.manifest"
                      "${root}/cmd/aisummoner-client/aisummoner-client.manifest")
    if(NOT EXISTS "${path}")
        message(FATAL_ERROR "required package source is missing: ${path}")
    endif()
endforeach()

execute_process(
    COMMAND "${MT_EXECUTABLE}" -nologo
            -manifest "${root}/desktop/remote-client/resources/aisummoner-client-ui.manifest"
            "-outputresource:${GUI_EXECUTABLE}"
    RESULT_VARIABLE gui_manifest_result
    OUTPUT_VARIABLE gui_manifest_output
    ERROR_VARIABLE gui_manifest_error
)
if(NOT gui_manifest_result EQUAL 0)
    message(FATAL_ERROR "embedding GUI asInvoker manifest failed: ${gui_manifest_output}${gui_manifest_error}")
endif()

execute_process(
    COMMAND "${MT_EXECUTABLE}" -nologo
            -manifest "${root}/cmd/aisummoner-client/aisummoner-client.manifest"
            "-outputresource:${CORE_EXECUTABLE}"
    RESULT_VARIABLE manifest_result
    OUTPUT_VARIABLE manifest_output
    ERROR_VARIABLE manifest_error
)
if(NOT manifest_result EQUAL 0)
    message(FATAL_ERROR "embedding Core asInvoker manifest failed: ${manifest_output}${manifest_error}")
endif()

file(REMOVE_RECURSE "${stage}")
file(MAKE_DIRECTORY "${stage}")
file(COPY "${GUI_EXECUTABLE}" "${CORE_EXECUTABLE}" DESTINATION "${stage}")
file(COPY "${root}/LICENSE" "${root}/THIRD_PARTY_NOTICES.md" DESTINATION "${stage}")
file(COPY "${root}/deploy/windows/README.txt"
          "${root}/deploy/windows/QT_SOURCE_OFFER.txt" DESTINATION "${stage}")

get_filename_component(qt_bin "${WINDEPLOYQT_EXECUTABLE}" DIRECTORY)
get_filename_component(qt_prefix "${qt_bin}" DIRECTORY)
set(qt_license_root "")
foreach(candidate IN ITEMS "${qt_prefix}/LICENSES" "${qt_prefix}/licenses"
                           "${qt_prefix}/../LICENSES" "${qt_prefix}/../../LICENSES")
    if(EXISTS "${candidate}/LGPL-3.0-only.txt")
        file(REAL_PATH "${candidate}" qt_license_root)
        break()
    endif()
endforeach()
if(qt_license_root STREQUAL "")
    message(FATAL_ERROR "Qt open-source license texts were not found beside windeployqt")
endif()
file(MAKE_DIRECTORY "${stage}/THIRD_PARTY_LICENSES/Qt")
file(COPY "${qt_license_root}/" DESTINATION "${stage}/THIRD_PARTY_LICENSES/Qt")

execute_process(
    COMMAND "${WINDEPLOYQT_EXECUTABLE}" --release --compiler-runtime --no-translations
            --dir "${stage}" "${stage}/aisummoner-client-ui.exe"
    RESULT_VARIABLE deploy_result
    OUTPUT_VARIABLE deploy_output
    ERROR_VARIABLE deploy_error
)
if(NOT deploy_result EQUAL 0)
    message(FATAL_ERROR "windeployqt failed: ${deploy_output}${deploy_error}")
endif()

foreach(required IN ITEMS
        aisummoner-client-ui.exe
        aisummoner-client.exe
        LICENSE
        THIRD_PARTY_NOTICES.md
        QT_SOURCE_OFFER.txt
        README.txt
        Qt6Core.dll
        Qt6Gui.dll
        Qt6Network.dll
        Qt6Widgets.dll
        platforms/qwindows.dll
        tls/qschannelbackend.dll
        THIRD_PARTY_LICENSES/Qt/LGPL-3.0-only.txt
        THIRD_PARTY_LICENSES/Qt/Qt-GPL-exception-1.0.txt
        vc_redist.x64.exe)
    if(NOT EXISTS "${stage}/${required}")
        message(FATAL_ERROR "Windows package is missing ${required}")
    endif()
endforeach()

foreach(executable IN ITEMS aisummoner-client-ui.exe aisummoner-client.exe)
    set(extracted_manifest "${dist}/.${executable}.manifest-check.xml")
    execute_process(
        COMMAND "${MT_EXECUTABLE}" -nologo
                "-inputresource:${stage}/${executable}"
                "-out:${extracted_manifest}"
        RESULT_VARIABLE extract_result
        OUTPUT_VARIABLE extract_output
        ERROR_VARIABLE extract_error
    )
    if(NOT extract_result EQUAL 0)
        message(FATAL_ERROR "extracting ${executable} manifest failed: ${extract_output}${extract_error}")
    endif()
    file(READ "${extracted_manifest}" embedded_manifest)
    file(REMOVE "${extracted_manifest}")
    string(FIND "${embedded_manifest}" "requestedExecutionLevel level=\"asInvoker\""
           as_invoker)
    if(as_invoker EQUAL -1)
        message(FATAL_ERROR "${executable} is missing its asInvoker manifest")
    endif()

    execute_process(
        COMMAND "${SIGNTOOL_EXECUTABLE}" verify /pa "${stage}/${executable}"
        RESULT_VARIABLE signature_result
        OUTPUT_QUIET ERROR_QUIET
    )
    if(signature_result EQUAL 0)
        message(FATAL_ERROR "engineering executable is unexpectedly signed: ${executable}")
    endif()
endforeach()

execute_process(COMMAND "${DUMPBIN_EXECUTABLE}" /headers
                "${stage}/aisummoner-client-ui.exe"
                RESULT_VARIABLE gui_headers_result OUTPUT_VARIABLE gui_headers ERROR_VARIABLE gui_headers_error)
if(NOT gui_headers_result EQUAL 0 OR NOT gui_headers MATCHES "subsystem \\(Windows GUI\\)")
    message(FATAL_ERROR "Qt executable is not Windows GUI subsystem: ${gui_headers_error}")
endif()
execute_process(COMMAND "${DUMPBIN_EXECUTABLE}" /headers
                "${stage}/aisummoner-client.exe"
                RESULT_VARIABLE core_headers_result OUTPUT_VARIABLE core_headers ERROR_VARIABLE core_headers_error)
if(NOT core_headers_result EQUAL 0 OR NOT core_headers MATCHES "subsystem \\(Windows CUI\\)")
    message(FATAL_ERROR "Core executable is not Windows console subsystem: ${core_headers_error}")
endif()

file(SHA256 "${stage}/aisummoner-client-ui.exe" GUI_SHA256)
file(SHA256 "${stage}/aisummoner-client.exe" CORE_SHA256)
configure_file("${root}/deploy/windows/package-build.json.in"
               "${stage}/package-build.json" @ONLY NEWLINE_STYLE UNIX)

file(GLOB_RECURSE package_files LIST_DIRECTORIES false RELATIVE "${stage}" "${stage}/*")
foreach(entry IN LISTS package_files)
    string(TOLOWER "${entry}" lower_entry)
    if(lower_entry MATCHES "(^|/)(windows-contract-probe|aisummoner_windows_.*probe)\\.exe$"
       OR lower_entry MATCHES "\\.(pdb|ilk|exp|lib|key|pem|token|cookie)$")
        message(FATAL_ERROR "forbidden engineering/private file in package: ${entry}")
    endif()
endforeach()

foreach(text_file IN ITEMS README.txt LICENSE THIRD_PARTY_NOTICES.md
                           QT_SOURCE_OFFER.txt package-build.json)
    file(READ "${stage}/${text_file}" text_contents)
    string(FIND "${text_contents}" "${root}" leaked_root)
    if(NOT leaked_root EQUAL -1)
        message(FATAL_ERROR "package text leaks build-host path: ${text_file}")
    endif()
endforeach()

get_filename_component(archive_directory "${ARCHIVE_PATH}" DIRECTORY)
file(MAKE_DIRECTORY "${archive_directory}")
file(REMOVE "${ARCHIVE_PATH}" "${ARCHIVE_PATH}.sha256")
file(ARCHIVE_CREATE OUTPUT "${ARCHIVE_PATH}"
     PATHS "AISummoner-Windows-Remote"
     FORMAT zip
     WORKING_DIRECTORY "${dist}")
file(SHA256 "${ARCHIVE_PATH}" archive_sha256)
get_filename_component(archive_name "${ARCHIVE_PATH}" NAME)
file(WRITE "${ARCHIVE_PATH}.sha256" "${archive_sha256}  ${archive_name}\n")

list(LENGTH package_files package_file_count)
message(STATUS "Windows package files: ${package_file_count}")
message(STATUS "Windows package SHA-256: ${archive_sha256}")
