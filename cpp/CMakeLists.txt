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
endif()
