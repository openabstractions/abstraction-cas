#pragma once
#include <abstraction/cas.h>
#include <abstraction/cas/api/rec.h>

namespace abstraction::cas {
// Adapts the existing provider, retaining Moved and filesystem exceptions.
class FileStore final : public api::Store {
public:
 api::Value Read(const std::string& path) override {
  auto current=read(std::filesystem::path(path));
  api::Value result;
  if(current) result.data=std::vector<std::uint8_t>(current->begin(),current->end());
  return result;
 }
 void Write(const std::string& path,const api::Value& base,const std::vector<std::uint8_t>& data) override {
  Value current;
  if(base.data) current=std::string(base.data->begin(),base.data->end());
  write(std::filesystem::path(path),current,std::string(data.begin(),data.end()));
 }
};
}
