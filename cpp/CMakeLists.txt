cmake_minimum_required(VERSION 3.16)

if(CMAKE_SOURCE_DIR STREQUAL CMAKE_CURRENT_SOURCE_DIR)
    project(abstraction_cas LANGUAGES CXX VERSION 0.1.0)
    set(ABSTRACTION_CAS_STANDALONE ON)
else()
    set(ABSTRACTION_CAS_STANDALONE OFF)
endif()

include(GNUInstallDirs)
find_package(Threads REQUIRED)

option(ABSTRACTION_BUILD_TESTS "Build the abstraction's own tests"
    ${ABSTRACTION_CAS_STANDALONE})

add_library(abstraction_cas STATIC src/cas.cpp)
target_include_directories(abstraction_cas PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
    $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)
target_compile_features(abstraction_cas PUBLIC cxx_std_17)
set_target_properties(abstraction_cas PROPERTIES EXPORT_NAME cas)
add_library(abstraction::cas ALIAS abstraction_cas)

if(ABSTRACTION_BUILD_TESTS)
    enable_testing()
    add_executable(test_cas test/test_cas.cpp)
    target_link_libraries(test_cas PRIVATE abstraction_cas Threads::Threads)
    add_test(NAME Cas COMMAND test_cas)
    add_executable(test_cas_api test/test_api.cpp)
    target_link_libraries(test_cas_api PRIVATE abstraction_cas Threads::Threads)
    add_test(NAME CasAPI COMMAND test_cas_api)
endif()

if(ABSTRACTION_CAS_STANDALONE)
    include(CMakePackageConfigHelpers)
    set(ABSTRACTION_CAS_CMAKEDIR ${CMAKE_INSTALL_LIBDIR}/cmake/abstraction_cas)
    install(TARGETS abstraction_cas EXPORT abstraction_casTargets
        ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
        LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
        RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
    install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
    install(DIRECTORY abstraction/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}/abstraction
        FILES_MATCHING PATTERN "*.h")
    install(FILES LICENSE DESTINATION ${CMAKE_INSTALL_DATADIR}/doc/abstraction_cas)
    configure_file(cmake/abstraction_cas.pc.in abstraction_cas.pc @ONLY)
    install(FILES ${CMAKE_CURRENT_BINARY_DIR}/abstraction_cas.pc
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/pkgconfig)
    install(EXPORT abstraction_casTargets FILE abstraction_casTargets.cmake
        NAMESPACE abstraction:: DESTINATION ${ABSTRACTION_CAS_CMAKEDIR})
    configure_package_config_file(cmake/abstraction_casConfig.cmake.in
        ${CMAKE_CURRENT_BINARY_DIR}/abstraction_casConfig.cmake
        INSTALL_DESTINATION ${ABSTRACTION_CAS_CMAKEDIR})
    write_basic_package_version_file(
        ${CMAKE_CURRENT_BINARY_DIR}/abstraction_casConfigVersion.cmake
        COMPATIBILITY SameMinorVersion)
    install(FILES ${CMAKE_CURRENT_BINARY_DIR}/abstraction_casConfig.cmake
        ${CMAKE_CURRENT_BINARY_DIR}/abstraction_casConfigVersion.cmake
        DESTINATION ${ABSTRACTION_CAS_CMAKEDIR})
endif()
