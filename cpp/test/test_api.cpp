#include <abstraction/cas_api.h>
#include <chrono>
int main(){
 auto dir=std::filesystem::temp_directory_path()/("cas-api-"+std::to_string(std::chrono::steady_clock::now().time_since_epoch().count()));
 std::filesystem::create_directory(dir);
 struct Cleanup { std::filesystem::path path; ~Cleanup(){std::filesystem::remove_all(path);} } cleanup{dir};
 auto path=(dir/"value").string();abstraction::cas::FileStore native;abstraction::cas::api::Store& api=native;
 auto absent=api.Read(path);if(absent.data)return 1;
 api.Write(path,absent,{});auto empty=api.Read(path);if(!empty.data||!empty.data->empty())return 2;
 std::vector<std::uint8_t> bytes{0,255,128,10};api.Write(path,empty,bytes);auto value=api.Read(path);if(!value.data||*value.data!=bytes)return 3;
 try{api.Write(path,absent,bytes);return 4;}catch(const abstraction::cas::Moved&){}
}
