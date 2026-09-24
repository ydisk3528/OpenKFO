// Experimental single-player practice MOD. Native creation runs on the game's
// window thread, not on the injector thread. No DLL or permanent hook is used.
#include <commctrl.h>
#include <functional>

struct ModContext { U scene=0,mode=0,npc=0; Actor player; std::vector<GuardWord> guards; };
bool practiceContext(ModContext& m,bool existingMod=false) {
    if(!session.current(m.player)||!read(session.process,session.base+0x13C8710,m.scene)||
       !read(session.process,m.scene+0xAC,m.mode))return false;
    U vt=0,phase=0;
    if(!read(session.process,m.scene+0x78,phase)||phase!=4||!read(session.process,m.mode,vt)||vt!=session.base+0x7AB6E4||
       !read(session.process,m.mode+0x10,m.npc))return false;
    U manager=0,head=0,root=0; if(!read(session.process,session.base+0x13C8708,manager)||!read(session.process,manager+0x2F0,head)||!read(session.process,head+4,root))return false;
    Actor npc;if(!resolve(session.process,manager,100,npc)||npc.ptr!=m.npc||!isNpc(npc))return false;
    std::vector<U> todo{root};std::set<U> seen;unsigned players=0,retired=0,activeNpcs=0;
    while(!todo.empty()) {
        U n=todo.back();todo.pop_back();if(!n||n==head)continue;
        if(!seen.insert(n).second||seen.size()>128)return false;
        struct Node {U left,parent,right,key,value;BYTE color,nil,pad[2];} v{};
        if(!read(session.process,n,v))return false;if(v.nil)continue;
        todo.push_back(v.left);todo.push_back(v.right);
        if(v.key<8&&v.value)players++;
        // One replacement per scene. Retired NPCs remain alive until scene teardown.
        if(v.key>=8&&v.key<40&&v.value){uint64_t uid=0;
            if(!read(session.process,v.value+0xC90,uid))return false;
            if(uid==100)activeNpcs++;else if(uid==0)retired++;else return false;}
    }
    if(players!=1||activeNpcs!=1||retired!=(existingMod?1U:0U))return false;
    m.guards={{session.base+0x13C8710,m.scene},{m.scene+0xAC,m.mode},{m.scene+0x78,phase},{m.mode,vt},{m.mode+0x10,m.npc},
      {m.player.ptr+0xC90,(U)m.player.uid},{m.player.ptr+0xC94,(U)(m.player.uid>>32)}};
    // Battle context must still match when the window thread executes the job.
    for(U off:{0x311U,0x315U}){U v=0;if(!read(session.process,manager+off,v))return false;m.guards.push_back({manager+off,v});}
    for(auto g:std::vector<GuardWord>{{session.base+0x556E10,0x51EC8B55},{session.base+0x693550,0x6AEC8B55},
        {session.base+0x693730,0x6AEC8B55},{session.base+0x57EE70,0x83EC8B55},{session.base+0x04E300,0x51EC8B55},{session.base+0x5E3FB0,0x51EC8B55},{session.base+0x5E48D0,0x83EC8B55},{session.base+0x5FF460,0x51EC8B55},{session.base+0x6015A0,0x6AEC8B55}}){
        U v=0;if(!read(session.process,g.address,v)||v!=g.value)return false;m.guards.push_back(g);}
    return true;
}

U remoteUser32(const char* name) {
    // GetProcAddress in this EXE can return an apphelp compatibility shim.
    // That address/RVA does not belong to USER32 and may be unmapped in the game.
    // Resolve the target's own export table; never transplant a local pointer.
    HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPMODULE,GetProcessId(session.process));MODULEENTRY32W m{};m.dwSize=sizeof(m);U base=0,size=0;
    if(snap!=INVALID_HANDLE_VALUE){if(Module32FirstW(snap,&m))do{if(!_wcsicmp(m.szModule,L"user32.dll")){base=(U)m.modBaseAddr;size=m.modBaseSize;break;}}while(Module32NextW(snap,&m));CloseHandle(snap);}
    if(!base||size<sizeof(IMAGE_DOS_HEADER))return 0;
    auto range=[=](U rva,size_t count){return rva<size&&count<=size-rva;};
    IMAGE_DOS_HEADER dos{};IMAGE_NT_HEADERS32 nt{};
    if(!read(session.process,base,dos)||dos.e_magic!=IMAGE_DOS_SIGNATURE||dos.e_lfanew<0||
       !range((U)dos.e_lfanew,sizeof(nt))||!read(session.process,base+dos.e_lfanew,nt)||
       nt.Signature!=IMAGE_NT_SIGNATURE||nt.OptionalHeader.Magic!=IMAGE_NT_OPTIONAL_HDR32_MAGIC)return 0;
    auto dir=nt.OptionalHeader.DataDirectory[IMAGE_DIRECTORY_ENTRY_EXPORT];IMAGE_EXPORT_DIRECTORY exports{};
    if(!dir.VirtualAddress||dir.Size<sizeof(exports)||!range(dir.VirtualAddress,dir.Size)||
       !read(session.process,base+dir.VirtualAddress,exports)||exports.NumberOfNames>65536||exports.NumberOfFunctions>65536||
       !range(exports.AddressOfNames,exports.NumberOfNames*4)||!range(exports.AddressOfNameOrdinals,exports.NumberOfNames*2)||
       !range(exports.AddressOfFunctions,exports.NumberOfFunctions*4))return 0;
    size_t nameLength=strlen(name)+1;if(nameLength>64)return 0;
    for(U i=0;i<exports.NumberOfNames;i++){
        U nameRva=0;if(!read(session.process,base+exports.AddressOfNames+i*4,nameRva)||!range(nameRva,nameLength))return 0;
        char exported[64]{};SIZE_T got=0;
        if(!ReadProcessMemory(session.process,(void*)(base+nameRva),exported,nameLength,&got)||got!=nameLength)return 0;
        if(memcmp(exported,name,nameLength))continue;
        WORD ordinal=0;U rva=0;
        if(!read(session.process,base+exports.AddressOfNameOrdinals+i*2,ordinal)||ordinal>=exports.NumberOfFunctions||
           !read(session.process,base+exports.AddressOfFunctions+ordinal*4,rva)||!rva||!range(rva,1)||
           (rva>=dir.VirtualAddress&&rva-dir.VirtualAddress<dir.Size))return 0; // Refuse forwarded exports.
        MEMORY_BASIC_INFORMATION info{};
        if(!VirtualQueryEx(session.process,(void*)(base+rva),&info,sizeof(info))||info.State!=MEM_COMMIT||
           (info.Protect&PAGE_GUARD)||!(info.Protect&(PAGE_EXECUTE|PAGE_EXECUTE_READ|PAGE_EXECUTE_READWRITE|PAGE_EXECUTE_WRITECOPY)))return 0;
        return base+rva;
    }
    return 0;
}
HWND gameWindow() {
    struct Find {DWORD pid;HWND window;LONG area;} f{GetProcessId(session.process),nullptr,0};
    EnumWindows([](HWND w,LPARAM l)->BOOL{auto& f=*(Find*)l;DWORD pid=0;GetWindowThreadProcessId(w,&pid);RECT r{};
        if(pid==f.pid&&IsWindowVisible(w)&&!GetWindow(w,GW_OWNER)&&GetClientRect(w,&r)){
            LONG area=r.right*r.bottom;if(area>f.area){f.area=area;f.window=w;}}
        return TRUE;},(LPARAM)&f);return f.window;
}

struct ModJob {
    HANDLE installer=nullptr;U block=0,callback=0,oldProc=0;HWND target=nullptr;bool sent=false;ULONGLONG started=0;
    bool busy()const{return block!=0;}
    bool start(const std::vector<GuardWord>& guards,const std::function<void(Code32&,U)>& body,HWND testWindow=nullptr) {
        if(busy()||killBusy()||!session.process)return false;
        target=testWindow?testWindow:gameWindow();if(!target)return false;
        U setter=remoteUser32("SetWindowLongW"),forward=remoteUser32("CallWindowProcW"),getter=remoteUser32("GetWindowLongW");
        if(!setter||!forward||!getter)return false;
        block=(U)VirtualAllocEx(session.process,nullptr,8192,MEM_RESERVE|MEM_COMMIT,PAGE_READWRITE);if(!block)return false;
        U data=block+4096;Code32 c;
        c.push((U)GWL_WNDPROC);c.push((U)target);c.call(getter);
        c.bytes({0xA3});c.word(data+8);c.bytes({0x85,0xC0,0x0F,0x84});size_t installBad=c.hole();
        c.bytes({0x68});size_t callbackWord=c.hole();c.push((U)GWL_WNDPROC);c.push((U)target);c.call(setter);
        c.bytes({0x85,0xC0,0x0F,0x84});size_t setterFailed=c.hole();
        c.bytes({0xA3});c.word(data+8);c.status(data+12,1);
        c.status(data,1);c.bytes({0x33,0xC0,0xC2,4,0});
        c.rel(installBad,c.b.size());c.rel(setterFailed,c.b.size());c.status(data,4);c.bytes({0x33,0xC0,0xC2,4,0});
        callback=block+(U)c.b.size();c.patch(callbackWord,callback);
        c.bytes({0x55,0x8B,0xEC,0x81,0x7D,0x0C});c.word(WM_APP+0x521);c.bytes({0x0F,0x85});size_t otherMsg=c.hole();
        c.bytes({0x81,0x7D,0x10});c.word(block);c.bytes({0x0F,0x85});size_t otherCookie=c.hole();
        c.bytes({0x60,0x83,0xEC,8,0xC7,0x44,0x24,4});size_t handler=c.hole();
        c.bytes({0x64,0xA1,0,0,0,0,0x89,0x04,0x24,0x64,0x89,0x25,0,0,0,0});
        c.bytes({0xFF,0x35});c.word(data+8);c.push((U)GWL_WNDPROC);c.push((U)target);c.call(setter);
        c.status(data,2);std::vector<size_t> stale;
        c.push((U)GWL_WNDPROC);c.push((U)target);c.call(getter);c.bytes({0x3B,0x05});c.word(data+8);c.bytes({0x0F,0x85});stale.push_back(c.hole());c.status(data+4,1);
        for(auto g:guards){c.bytes({0x81,0x3D});c.word(g.address);c.word(g.value);c.bytes({0x0F,0x85});stale.push_back(c.hole());}
        body(c,data);c.bytes({0xE9});size_t done=c.hole();
        size_t skip=c.b.size();c.status(data,4);c.bytes({0xE9});size_t skipped=c.hole();
        size_t fault=c.b.size();c.status(data,6);
        size_t cleanup=c.b.size();
        c.bytes({0x8B,0x04,0x24,0x64,0xA3,0,0,0,0,0x83,0xC4,8,0x61,0x33,0xC0,0x8B,0xE5,0x5D,0xC2,0x10,0});
        c.rel(done,cleanup);c.rel(skipped,cleanup);for(auto x:stale)c.rel(x,skip);
        c.rel(otherMsg,c.b.size());c.rel(otherCookie,c.b.size());
        c.bytes({0xFF,0x75,0x14,0xFF,0x75,0x10,0xFF,0x75,0x0C,0xFF,0x75,8,0xFF,0x35});c.word(data+8);c.call(forward);c.bytes({0x8B,0xE5,0x5D,0xC2,0x10,0});
        c.patch(handler,block+(U)c.b.size());
        c.bytes({0x8B,0x44,0x24,0x0C,0x8B,0x54,0x24,8,0x89,0x90});c.word(0xC4);
        c.bytes({0xC7,0x80});c.word(0xB8);c.word(block+(U)fault);c.bytes({0x33,0xC0,0xC3});
        SIZE_T n=0;DWORD previous=0;bool ok=c.b.size()<4096&&WriteProcessMemory(session.process,(void*)block,c.b.data(),c.b.size(),&n)&&n==c.b.size()&&
          VirtualProtectEx(session.process,(void*)block,4096,PAGE_EXECUTE_READ,&previous)&&FlushInstructionCache(session.process,(void*)block,c.b.size());
        if(ok)installer=CreateRemoteThread(session.process,nullptr,0,(LPTHREAD_START_ROUTINE)block,nullptr,0,nullptr);
        if(!installer){VirtualFreeEx(session.process,(void*)block,0,MEM_RELEASE);block=0;return false;}
        sent=false;started=GetTickCount64();return true;
    }
    int poll() {
        if(!busy())return -2;
        if(WaitForSingleObject(session.process,0)==WAIT_OBJECT_0){if(installer)CloseHandle(installer);installer=nullptr;block=0;return 6;}
        if(installer){if(WaitForSingleObject(installer,0)!=WAIT_OBJECT_0)return -1;CloseHandle(installer);installer=nullptr;}
        U result=0;if(!read(session.process,block+4096,result))return -1;
        if(result==1&&!sent){sent=true;DWORD_PTR response=0;SendMessageTimeoutW(target,WM_APP+0x521,block,0,SMTO_ABORTIFHUNG|SMTO_BLOCK,300,&response);return -1;}
        if(result<3)return -1;
        // A completion flag alone is insufficient: drain the window callback
        // before releasing its machine code. Never terminate a stuck thread.
        DWORD_PTR response=0;
        U restored=0,installed=0;if(!read(session.process,block+4100,restored)||!read(session.process,block+4108,installed))return -1;
        if(IsWindow(target)&&((installed&&!restored)||!SendMessageTimeoutW(target,WM_NULL,0,0,SMTO_ABORTIFHUNG|SMTO_BLOCK,100,&response)))return -1;
        VirtualFreeEx(session.process,(void*)block,0,MEM_RELEASE);block=0;return (int)result;
    }
} modJob;

HWND modStatus=nullptr;
void startPracticeMod(bool restore) {
    if(restore){SetWindowTextW(modStatus,L"请退出训练再重新进入，以恢复原版 NPC；本实验每局只允许生成一次。");return;}
    ModContext m;
    if(!practiceContext(m)){SetWindowTextW(modStatus,L"仅支持单人自由训练，每局只能生成一次。重复测试请退出训练再进入。");return;}
    if(checked(killButton)||checked(poisonCheck)){SetWindowTextW(modStatus,L"请先取消秒杀和 NPC 中毒，避免新怪物立即死亡。");return;}
    U base=session.base;
    // Follow PVE 943230 retirement: detach while UID is still unique, disable,
    // then reset UID. Never destroy an actor referenced by the current frame.
    // Do this BEFORE the factory registers its replacement (which also uses UID 100).
    bool ok=modJob.start(m.guards,[=](Code32& c,U data){
        const char model[]="M_NPC_EGUN",ai[]="pve008";
        for(U i=0;i<sizeof(model);i+=4){U v=0;memcpy(&v,model+i,(sizeof(model)-i<4)?sizeof(model)-i:4);c.status(data+64+i,v);}
        for(U i=0;i<sizeof(ai);i+=4){U v=0;memcpy(&v,ai+i,(sizeof(ai)-i<4)?sizeof(ai)-i:4);c.status(data+96+i,v);}
        c.push(m.npc);c.bytes({0xB9});c.word(m.scene);c.call(base+0x04E300);
        c.bytes({0x8B,0xC8});c.call(base+0x57EE70);
        c.push(0);c.bytes({0xB9});c.word(m.npc);c.call(base+0x5E3FB0);
        c.push(0);c.push(0);c.bytes({0xB9});c.word(m.npc);c.call(base+0x5E48D0);
        c.status(m.mode+0x10,0);
        c.push(0);c.push(0);c.push(m.mode+0x18);c.bytes({0xFF,0x35});c.word(m.mode+0x14);c.push(data+64);
        c.call(base+0x693550);c.bytes({0x8B,0xC8});c.call(base+0x693730);
        c.bytes({0x85,0xC0,0x0F,0x84});size_t failed=c.hole();
        c.bytes({0xA3});c.word(data+16);
        c.bytes({0xA1});c.word(data+16);c.bytes({0xA3});c.word(m.mode+0x10);
        c.push(0);c.push(100);c.bytes({0x8B,0xC8});c.call(base+0x5E48D0);
        c.bytes({0xA1});c.word(data+16);c.bytes({0xC7,0x40,0x28});c.word(0xFFFFFFFF);
        c.push(data+96);c.bytes({0x8B,0xC8});c.call(base+0x6015A0);
        c.bytes({0x84,0xC0,0x0F,0x84});size_t aiFail=c.hole();
        c.bytes({0x8B,0x0D});c.word(data+16);c.call(base+0x5FF460);
        c.status(data,3);c.bytes({0xE9});size_t finish=c.hole();
        c.rel(aiFail,c.b.size());c.status(data,5);c.bytes({0xE9});size_t aiDone=c.hole();
        c.rel(failed,c.b.size());c.status(data,5);c.rel(finish,c.b.size());c.rel(aiDone,c.b.size());
    });
    SetWindowTextW(modStatus,ok?L"正在游戏主线程创建 / 恢复 NPC，请保持在地图内。":L"未执行：已有操作正在运行，或无法连接游戏窗口。");
}

// Re-select native PVE AI without replacing or freeing the current actor.
void startPracticeAi() {
    ModContext m;
    if(!practiceContext(m,true)){SetWindowTextW(modStatus,L"请先在当前单人训练中生成 MOD 怪物。");return;}
    U base=session.base;
    bool ok=modJob.start(m.guards,[=](Code32& c,U data){
        const char ai[]="pve008";
        for(U i=0;i<sizeof(ai);i+=4){U v=0;memcpy(&v,ai+i,(sizeof(ai)-i<4)?sizeof(ai)-i:4);c.status(data+96+i,v);}
        c.push(data+96);c.bytes({0xB9});c.word(m.npc);c.call(base+0x6015A0);
        c.bytes({0x84,0xC0,0x0F,0x84});size_t failed=c.hole();
        c.bytes({0xB9});c.word(m.npc);c.call(base+0x5FF460);
        c.status(data,3);c.bytes({0xE9});size_t done=c.hole();
        c.rel(failed,c.b.size());c.status(data,5);c.rel(done,c.b.size());
    });
    SetWindowTextW(modStatus,ok?L"正在启用自主 AI。请勿选择原版训练行为，避免覆盖 AI。":L"已有操作正在执行或游戏窗口不可用。");
}
