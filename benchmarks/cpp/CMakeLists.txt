cmake_minimum_required(VERSION 3.23)
project(mfast_bench CXX)

set(BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(BUILD_PACKAGES OFF CACHE BOOL "" FORCE)
add_subdirectory(third_party/mFAST EXCLUDE_FROM_ALL)

# Static style: generate C++ types from the same template XML with fast_type_gen.
set(XML ${CMAKE_CURRENT_SOURCE_DIR}/../testdata/templates.xml)
set(GEN ${CMAKE_CURRENT_BINARY_DIR}/templates.cpp ${CMAKE_CURRENT_BINARY_DIR}/templates.h
        ${CMAKE_CURRENT_BINARY_DIR}/templates.inl)
add_custom_command(OUTPUT ${GEN}
  COMMAND $<TARGET_FILE:fast_type_gen> -- ${XML}
  DEPENDS ${XML} fast_type_gen
  WORKING_DIRECTORY ${CMAKE_CURRENT_BINARY_DIR})

add_executable(mfast_bench bench.cpp ${GEN})
target_include_directories(mfast_bench PRIVATE ${CMAKE_CURRENT_BINARY_DIR})
target_link_libraries(mfast_bench PRIVATE mfast_xml_parser_static mfast_coder_static mfast_static)
