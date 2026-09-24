#define UNICODE
#define _UNICODE
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <tlhelp32.h>
#include <wincrypt.h>
#include <stdint.h>
#include <string>
#include <vector>
#include <set>
#include <cmath>
#include <cstdio>
#include <cstring>

using U = uint32_t;
static_assert(sizeof(void*) == 4, "Build Win32/x86");
template<class T> bool read(HANDLE h, U a, T& v) {
    SIZE_T n=0; return a>=0x10000 && ReadProcessMemory(h,(void*)a,&v,sizeof(v),&n) && n==sizeof(v);
}
bool write(HANDLE h,U a,U v) {
    SIZE_T n=0; return a>=0x10000 && WriteProcessMemory(h,(void*)a,&v,4,&n) && n==4;
}
struct Actor { U ptr=0,state=0; uint64_t uid=0; };
// gfxz 1B7C8676: 818C70 finds an actor by UID in manager+2D8.
// Debug MSVC tree: head +18, node left/parent/right +0/+4/+8,
// key/value +C/+10, nil +15. No call into the game is required.
bool resolve(HANDLE h,U manager,uint64_t uid,Actor& out) {
    U head=0,root=0; if(!uid || !read(h,manager+0x2F0,head)||!read(h,head+4,root))return false;
    std::vector<U> pending{root}; std::set<U> seen;
    while(!pending.empty() && seen.size()<128) {
        U node=pending.back();pending.pop_back();
        if(!node || node==head || !seen.insert(node).second)continue;
        struct Node {U left,parent,right,key,value; BYTE color,nil; BYTE pad[2];} n{};
        if(!read(h,node,n))return false;
        if(n.nil)continue;
        pending.push_back(n.left);pending.push_back(n.right);
        uint64_t found=0;
        if(n.key<40 && n.value && read(h,n.value+0xC90,found) && found==uid) {
            U state=0; if(!read(h,n.value+0x1A6C,state))return false;
            out={n.value,state,uid};return true;
        }
    }
    return false;
}
struct Session {
    HANDLE process=nullptr; U base=0; Actor actor;
    bool current(Actor& a) {
        U manager=0,info=0;uint64_t uid=0;
        return process && WaitForSingleObject(process,0)==WAIT_TIMEOUT &&
          read(process,base+0x13C8708,manager)&&read(process,base+0x13C86FC,info)&&
          read(process,info+0x18,uid)&&resolve(process,manager,uid,a);
    }
    bool same() {
        Actor a;return current(a)&&a.ptr==actor.ptr&&a.state==actor.state&&a.uid==actor.uid;
    }
} session;
struct Feature {
    const wchar_t* name;U offset;bool state,isInt;const wchar_t* initial;float scale;
    HWND check=nullptr,edit=nullptr,value=nullptr;bool saved=false;U original=0,last=0;
} features[]={
 {L"怒气倍率",0x80,true,false,L"999",1},
 {L"无敌",0x1780,false,true,L"1",1},
 {L"霸体",0x64,true,true,L"1",1},
 {L"反伤值",0x1BD0,false,false,L"1",1},
 {L"防御值",0x78,true,false,L"1",1},
 {L"吸血值（原版输入单位）",0x6C,true,false,L"10",0.1f}
};
HWND window,combo,status;HFONT font;bool ticking=false;
#include "kill.h"
#include "mod.h"
#include "team_mod.h"
#include "identity.h"
#include "team_auto.h"
HWND monsterPreview=nullptr;HBITMAP monsterBitmaps[2]{};
void refreshMonsterPreview(){LRESULT i=SendMessageW(monsterChoice,CB_GETCURSEL,0,0);if(i>=0&&i<2)SendMessageW(monsterPreview,STM_SETIMAGE,IMAGE_BITMAP,(LPARAM)monsterBitmaps[i]);}
HWND tabs=nullptr;std::vector<HWND> featureControls,modControls;
void selectTab(int index) {
    for(HWND c:featureControls)ShowWindow(c,index==0?SW_SHOW:SW_HIDE);
    for(HWND c:modControls)ShowWindow(c,index==1?SW_SHOW:SW_HIDE);
}
std::wstring settingsPath;
bool loadingSettings=true,attributeFault=false,connectPending=false,closePending=false,disconnectPending=false;
void saveSettings() {
    if(loadingSettings||settingsPath.empty())return;
    int i=0;for(auto& f:features){wchar_t key[32],value[80];swprintf_s(key,L"enabled%d",i);WritePrivateProfileStringW(L"Options",key,checked(f.check)?L"1":L"0",settingsPath.c_str());
        swprintf_s(key,L"value%d",i++);GetWindowTextW(f.edit,value,80);WritePrivateProfileStringW(L"Options",key,value,settingsPath.c_str());}
    WritePrivateProfileStringW(L"Options",L"kill",checked(killButton)?L"1":L"0",settingsPath.c_str());
    WritePrivateProfileStringW(L"Options",L"poison",checked(poisonCheck)?L"1":L"0",settingsPath.c_str());
}
void loadSettings() {
    loadingSettings=true;
    if(settingsPath.empty()) {
        wchar_t dir[32768];DWORD len=GetEnvironmentVariableW(L"LOCALAPPDATA",dir,32768);
        if(!len||len>=32768){loadingSettings=false;return;}
        std::wstring root=std::wstring(dir)+L"\\OpenKFO";CreateDirectoryW(root.c_str(),nullptr);settingsPath=root+L"\\ScienceExe.ini";
    }
    int i=0;for(auto& f:features){wchar_t key[32],value[80];swprintf_s(key,L"enabled%d",i);SendMessageW(f.check,BM_SETCHECK,GetPrivateProfileIntW(L"Options",key,0,settingsPath.c_str())==1?BST_CHECKED:BST_UNCHECKED,0);
        swprintf_s(key,L"value%d",i++);GetPrivateProfileStringW(L"Options",key,f.initial,value,80,settingsPath.c_str());SetWindowTextW(f.edit,value);}
    SendMessageW(killButton,BM_SETCHECK,GetPrivateProfileIntW(L"Options",L"kill",0,settingsPath.c_str())==1?BST_CHECKED:BST_UNCHECKED,0);
    SendMessageW(poisonCheck,BM_SETCHECK,GetPrivateProfileIntW(L"Options",L"poison",0,settingsPath.c_str())==1?BST_CHECKED:BST_UNCHECKED,0);
    loadingSettings=false;
}
void message(const wchar_t* s){SetWindowTextW(status,s);}
U address(const Feature& f){return (f.state?session.actor.state:session.actor.ptr)+f.offset;}
bool restore(Feature& f) {
    if(!f.saved)return true;
    bool ok=true; U v=0;
    if(session.same()) {
        ok=read(session.process,address(f),v);
        // If the game changed the field itself, leave its newer value alone.
        if(ok && v==f.last)ok=write(session.process,address(f),f.original);
    }
    if(ok)f.saved=false;return ok;
}
bool stop() {
    killCancel=true;
    bool ok=true;
    for(auto& f:features){if(!restore(f))ok=false;}
    return ok;
}
bool disconnect() {
    autoPaused=true;
    if(modJob.busy()){disconnectPending=true;message(L"等待 MOD 主线程操作结束后断开。");return false;}
    if(!stop()){message(L"恢复原值失败，请保持游戏运行后重试停止。");return false;}
    if(killBusy()){disconnectPending=true;message(L"已取消后续目标，等待当前调用返回后自动断开或切换。");return false;}
    disconnectPending=false;cancelTeamAuto();if(session.process)CloseHandle(session.process);session={};poisonStamps.clear();updateIdentity(true);return true;
}
std::wstring hashFile(const wchar_t* path) {
    HANDLE file=CreateFileW(path,GENERIC_READ,FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,nullptr,OPEN_EXISTING,0,nullptr);
    if(file==INVALID_HANDLE_VALUE)return L"";
    HCRYPTPROV provider=0;HCRYPTHASH hash=0;std::wstring result;
    if(CryptAcquireContextW(&provider,nullptr,nullptr,PROV_RSA_AES,CRYPT_VERIFYCONTEXT)&&CryptCreateHash(provider,CALG_SHA_256,0,0,&hash)) {
        BYTE buffer[65536];DWORD n=0;bool ok=true;
        for(;;){if(!ReadFile(file,buffer,sizeof(buffer),&n,nullptr)){ok=false;break;}if(!n)break;if(!CryptHashData(hash,buffer,n,0)){ok=false;break;}}
        BYTE digest[32];DWORD size=32;
        if(ok&&CryptGetHashParam(hash,HP_HASHVAL,digest,&size,0)){wchar_t hex[3];for(BYTE b:digest){swprintf_s(hex,L"%02X",b);result+=hex;}}
    }
    if(hash)CryptDestroyHash(hash);if(provider)CryptReleaseContext(provider,0);CloseHandle(file);return result;
}
void refresh() {
    SendMessageW(combo,CB_RESETCONTENT,0,0);
    HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0);PROCESSENTRY32W p{};p.dwSize=sizeof(p);
    if(snap!=INVALID_HANDLE_VALUE){if(Process32FirstW(snap,&p))do{
        if(_wcsicmp(p.szExeFile,L"gfxz.dat")&&_wcsicmp(p.szExeFile,L"gfld.dat"))continue;
        wchar_t label[300];swprintf_s(label,L"%s  ·  PID %lu",p.szExeFile,p.th32ProcessID);
        LRESULT i=SendMessageW(combo,CB_ADDSTRING,0,(LPARAM)label);SendMessageW(combo,CB_SETITEMDATA,i,p.th32ProcessID);
    }while(Process32NextW(snap,&p));CloseHandle(snap);}
    SendMessageW(combo,CB_SETCURSEL,0,0);
}
void connect() {
    if(!disconnect()){connectPending=killBusy()||modJob.busy();return;}
    connectPending=false;
    LRESULT index=SendMessageW(combo,CB_GETCURSEL,0,0);if(index==CB_ERR){message(L"请先启动游戏，再刷新列表。");return;}
    DWORD pid=(DWORD)SendMessageW(combo,CB_GETITEMDATA,index,0);
    HANDLE h=OpenProcess(PROCESS_QUERY_INFORMATION|PROCESS_VM_READ|PROCESS_VM_WRITE|PROCESS_VM_OPERATION|PROCESS_CREATE_THREAD|SYNCHRONIZE,FALSE,pid);
    if(!h){message(L"无法打开进程。若游戏以管理员运行，请同样以管理员运行本工具。");return;}
    wchar_t path[32768];DWORD length=32768;
    if(!QueryFullProcessImageNameW(h,0,path,&length)||hashFile(path)!=L"1B7C8676E778C7BD47F55184F927338DA3CF70C6AA5F0BEB59869EA0F0AC9D4E") {
        CloseHandle(h);message(L"此客户端版本尚未核对地址，已拒绝写入。目前支持本机这份 gfxz.dat。");return;
    }
    HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPMODULE,pid);MODULEENTRY32W m{};m.dwSize=sizeof(m);U base=0;
    if(snap!=INVALID_HANDLE_VALUE){if(Module32FirstW(snap,&m))base=(U)m.modBaseAddr;CloseHandle(snap);}
    if(!base){CloseHandle(h);message(L"读取游戏模块失败。");return;}
    session.process=h;session.base=base;autoFault=attributeFault=autoPaused=false;lastRound=0;updateIdentity(true);message(L"已连接。沿用勾选设置，进入战斗后自动执行。");
}
bool input(Feature& f,U& bits) {
    if(f.isInt){bits=1;return true;}
    wchar_t text[80],*end;GetWindowTextW(f.edit,text,80);double v=wcstod(text,&end);
    while(*end==L' ')++end;
    if(end==text||*end||!std::isfinite(v)||v<0||v>999)return false;
    float actual=(float)v*f.scale;memcpy(&bits,&actual,4);return true;
}
void tick() {
    if(ticking||!session.process)return;ticking=true;
    updateIdentity();
    if(!connectPending&&!closePending&&!disconnectPending)tickTeamAuto();
    if(modJob.busy()){
        int r=modJob.poll();
        if(r>=3&&teamAuto.requesting){finishTeamRequest(r);teamOperation=false;ticking=false;return;}
        if(r>=3){SetWindowTextW(modStatus,r==3?(teamOperation?L"团队操作已完成。每个参战窗口先生成，全部登记后由房主启用 AI；尚需实测同步。":L"自主 AI 调用完成。靠近怪物测试；请勿点击原版训练行为选项。退出训练恢复原版。"):r==4?L"场景已变化，本次操作已取消。":r==5?L"原版模型或 AI 加载失败，请退出本局后重新进入。":L"原生调用异常，停止继续操作并重新进入游戏。");teamOperation=false;}
        ticking=false;return;
    }
    pollKill();
    if(!killBusy()&&(connectPending||closePending||disconnectPending)) {
        ticking=false;
        if(closePending){if(disconnect())DestroyWindow(window);}else if(connectPending)connect();else if(disconnect())message(L"已停止并断开，勾选已保留。");return;
    }
    if(teamAuto.busy()){ticking=false;return;}
    Actor a;
    if(!session.current(a)||!a.state) {
        stop();session.actor={};
        poisonStamps.clear();for(auto& f:features){SetWindowTextW(f.value,L"—");}
        message(L"等待自己的战斗角色。大厅、载入或离开战斗时停止修改。");ticking=false;return;
    }
    if(a.ptr!=session.actor.ptr||a.state!=session.actor.state||a.uid!=session.actor.uid) {
        stop();poisonStamps.clear();session.actor=a;
    }
    if(autoPaused){message(L"已停止，等待当前调用结束后断开；勾选设置保留。");ticking=false;return;}
    if(attributeFault){message(L"属性操作已暂停：检查输入值或重新连接后恢复，勾选已保留。");ticking=false;return;}
    bool error=false;
    for(auto& f:features) {
        EnableWindow(f.check,TRUE);U v=0;
        if(!read(session.process,address(f),v)){error=true;continue;}
        wchar_t text[80];if(f.isInt)swprintf_s(text,L"%lu",(unsigned long)v);else{float fv;memcpy(&fv,&v,4);swprintf_s(text,L"%.4g",fv);}SetWindowTextW(f.value,text);
        if(SendMessageW(f.check,BM_GETCHECK,0,0)==BST_CHECKED) {
            U desired=0;
            if(!input(f,desired)){restore(f);error=true;continue;}
            if(!session.same()){error=true;break;}
            if(!f.saved){f.original=v;f.last=v;f.saved=true;}
            if(write(session.process,address(f),desired))f.last=desired;else error=true;
        }else if(!restore(f))error=true;
    }
    if(error){stop();attributeFault=true;message(L"已暂停：输入须为 0–999，或角色状态已变化 / 内存读写失败。");}
    else {wchar_t text[160];swprintf_s(text,L"已找到自己的战斗角色 · 0x%08X\r\n每 100 毫秒检查；取消勾选或停止时尝试恢复启用前的值。",a.ptr);message(text);}
    if(!error)autoTick();
    ticking=false;
}
HWND control(const wchar_t* cls,const wchar_t* text,DWORD style,int x,int y,int w,int h,int id=0) {
    HWND c=CreateWindowW(cls,text,WS_CHILD|WS_VISIBLE|style,x,y,w,h,window,(HMENU)id,GetModuleHandleW(nullptr),nullptr);SendMessageW(c,WM_SETFONT,(WPARAM)font,TRUE);return c;
}
LRESULT CALLBACK proc(HWND h,UINT msg,WPARAM w,LPARAM l) {
    if(msg==WM_COMMAND&&teamAuto.busy()&&LOWORD(w)>=30&&LOWORD(w)<=33){SetWindowTextW(modStatus,L"正在等待本次服务器生成任务完成，请勿重复操作。");return 0;}
    if(msg==WM_NOTIFY&&((NMHDR*)l)->hwndFrom==tabs&&((NMHDR*)l)->code==TCN_SELCHANGE){selectTab(TabCtrl_GetCurSel(tabs));return 0;}
    if(msg==WM_COMMAND){switch(LOWORD(w)){case 34:if(HIWORD(w)==CBN_SELCHANGE)refreshMonsterPreview();break;case 32:startTeamUI(false);break;case 33:startTeamUI(true);break;case 30:startPracticeMod(false);break;case 31:startPracticeAi();break;case 10:refresh();break;case 11:connect();break;case 12:connectPending=false;if(disconnect())message(L"已停止并断开，勾选已保留。");break;default:
        if(HIWORD(w)==BN_CLICKED||HIWORD(w)==EN_CHANGE){saveSettings();attributeFault=false;}break;}return 0;}
    if(msg==WM_TIMER){tick();return 0;}
    if(msg==WM_CLOSE){saveSettings();closePending=true;if(disconnect())DestroyWindow(h);return 0;}
    if(msg==WM_DESTROY){for(auto b:monsterBitmaps)if(b)DeleteObject(b);PostQuitMessage(0);return 0;}
    return DefWindowProcW(h,msg,w,l);
}
int selfTest() {
    HANDLE h=GetCurrentProcess();auto* mem=(BYTE*)VirtualAlloc(nullptr,0x1400000,MEM_COMMIT|MEM_RESERVE,PAGE_READWRITE);if(!mem)return 1;
    U b=(U)mem,manager=b+0x1000,head=b+0x2000,node=b+0x2100,actor=b+0x3000,state=b+0x6000,info=b+0x7000;uint64_t uid=123456789;
    auto put=[&](U a,U v){memcpy((void*)a,&v,4);};
    put(manager+0x2F0,head);put(head+4,node);put(node,head);put(node+8,head);put(node+12,0);put(node+16,actor);memcpy((void*)(actor+0xC90),&uid,8);put(actor+0x1A6C,state);
    Actor a;bool ok=resolve(h,manager,uid,a)&&a.ptr==actor&&a.state==state;
    ok=ok&&!resolve(h,manager,uid+1,a)&&!resolve(h,0,uid,a);
    // Cyclic malformed tree must terminate.
    put(node,node);ok=ok&&!resolve(h,manager,uid+1,a);
    U original=17,v=0;put(state+0x80,original);ok=ok&&write(h,state+0x80,22)&&read(h,state+0x80,v)&&v==22&&write(h,state+0x80,original)&&read(h,state+0x80,v)&&v==17;
    put(b+0x13C8708,manager);put(b+0x13C86FC,info);memcpy((void*)(info+0x18),&uid,8);
    session.process=h;session.base=b;session.actor={actor,state,uid};
    Feature f=features[0];f.saved=true;f.original=17;f.last=22;put(state+0x80,22);
    ok=ok&&session.same()&&restore(f)&&read(h,state+0x80,v)&&v==17&&!f.saved;
    f.saved=true;put(state+0x80,33);ok=ok&&restore(f)&&read(h,state+0x80,v)&&v==33;
    f.saved=true;put(actor+0x1A6C,0);put(state+0x80,22);ok=ok&&restore(f)&&read(h,state+0x80,v)&&v==22;
    // Verify native packet layout, byte-offset round context and enemy filters.
    put(actor+0x1A6C,state);put(actor+0x28,1);put(actor+0x7C,1);put(actor+0xCE0,1);
    Actor enemy{b+0x9000,b+0xC000,uid+1};
    memcpy((void*)(enemy.ptr+0xC90),&enemy.uid,8);put(enemy.ptr+0x1A6C,enemy.state);
    put(enemy.ptr+0x28,2);put(enemy.ptr+0x7C,1);put(enemy.ptr+0xCE0,1);put(enemy.ptr+0xCE4,7);
    uint64_t context=0x1234567800000007ULL;memcpy((void*)(manager+0x311),&context,8);
    put(b+0x63FBB0,0x51EC8B55);put(b+0x5F29F0,0x83EC8B55);put(b+0x5E45B0,0x81EC8B55);
    KillCall call;ok=ok&&makeKill(enemy,call)&&call.source==actor&&call.target==enemy.ptr&&call.bits==0x461C3C00&&call.packet[55]==7&&call.packet[65]==1;
    uint64_t packetUid=0,packetContext=0;memcpy(&packetUid,call.packet+39,8);memcpy(&packetContext,call.packet+86,8);
    ok=ok&&packetUid==enemy.uid&&packetContext==context;
    put(enemy.ptr+0x28,1);KillCall excluded;ok=ok&&!makeKill(enemy,excluded);
    put(enemy.ptr+0x28,2);put(enemy.ptr+0x7C,0);ok=ok&&!makeKill(enemy,excluded);
    ok=ok&&!makeKill(session.actor,excluded);
    put(enemy.ptr+0x7C,1);
    // Exact CNpc RTTI classification, poison packet and native apply parameters.
    U vt=b+0xD100,col=b+0xD200;put(enemy.ptr,vt);put(vt-4,col);put(col+12,b+0xEF6B00);
    put(enemy.state,enemy.ptr);put(b+0x5C69C0,0x81EC8B55);put(b+0x5C5580,0x83EC8B55);put(b+0x5E7480,0x51EC8B55);
    KillCall poison;ok=ok&&isNpc(enemy)&&makeKill(enemy,poison,true)&&poison.poison&&poison.effect[0]==1&&poison.effect[1]==1&&poison.effect[2]==3000&&poison.effect[4]==(U)uid;
    U effectCode=0,operation=0;memcpy(&effectCode,poison.packet+55,4);memcpy(&operation,poison.packet+75,4);memcpy(&packetContext,poison.packet+79,8);
    ok=ok&&effectCode==1&&operation==1&&packetContext==context;
    put(col+12,b+0xEF6B04);ok=ok&&!isNpc(enemy)&&!makeKill(enemy,excluded,true);
    session={};
    VirtualFree(mem,0,MEM_RELEASE);return ok&&killSelfTest()&&teamPlanSelfTest()?0:2;
}
int inspect(DWORD pid) {
    HANDLE h=OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|PROCESS_VM_READ|SYNCHRONIZE,FALSE,pid);if(!h)return 3;
    wchar_t path[32768];DWORD size=32768;
    if(!QueryFullProcessImageNameW(h,0,path,&size)||hashFile(path)!=L"1B7C8676E778C7BD47F55184F927338DA3CF70C6AA5F0BEB59869EA0F0AC9D4E"){CloseHandle(h);return 4;}
    HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPMODULE,pid);MODULEENTRY32W m{};m.dwSize=sizeof(m);U base=0;
    if(snap!=INVALID_HANDLE_VALUE){if(Module32FirstW(snap,&m))base=(U)m.modBaseAddr;CloseHandle(snap);}
    session.process=h;session.base=base;Actor a;bool found=base&&session.current(a);
    char out[320];sprintf_s(out,"pid=%lu readonly=true actor_found=%d actor=0x%08X state=0x%08X GetWindowLongW=0x%08X SetWindowLongW=0x%08X CallWindowProcW=0x%08X\r\n",pid,found?1:0,a.ptr,a.state,remoteUser32("GetWindowLongW"),remoteUser32("SetWindowLongW"),remoteUser32("CallWindowProcW"));
    DWORD written;WriteFile(GetStdHandle(STD_OUTPUT_HANDLE),out,(DWORD)strlen(out),&written,nullptr);
    CloseHandle(h);session={};return found?0:5;
}
int WINAPI wWinMain(HINSTANCE instance,HINSTANCE,LPWSTR args,int show) {
    if(!wcscmp(args,L"--self-test"))return selfTest();
    DWORD pid=0;if(swscanf_s(args,L"--inspect %lu",&pid)==1)return inspect(pid);
    bool modCLI=swscanf_s(args,L"--practice-mod %lu",&pid)==1;
    bool restoreCLI=!modCLI&&swscanf_s(args,L"--practice-restore %lu",&pid)==1;
    bool aiCLI=swscanf_s(args,L"--practice-ai %lu",&pid)==1;
    U serial=0;bool teamCLI=swscanf_s(args,L"--team-probe %lu %u",&pid,&serial)==2;
    bool teamCreateCLI=swscanf_s(args,L"--team-create %lu %u",&pid,&serial)==2;
    bool teamAiCLI=swscanf_s(args,L"--team-ai %lu %u",&pid,&serial)==2;
    if(modCLI||restoreCLI||aiCLI||teamCLI||teamCreateCLI||teamAiCLI){
        // A second coordinator must never install another callback in this PID.
        // Keep the round claim until process exit; no automatic retry in a round.
        HANDLE replicaClaim=nullptr;
        if(teamCreateCLI){wchar_t name[120];swprintf_s(name,L"Local\\OpenKFO-TeamReplica-%lu-%u",pid,serial);
            replicaClaim=CreateMutexW(nullptr,FALSE,name);if(!replicaClaim||GetLastError()==ERROR_ALREADY_EXISTS)return 25;}
        HANDLE h=OpenProcess(PROCESS_QUERY_INFORMATION|PROCESS_VM_READ|PROCESS_VM_WRITE|PROCESS_VM_OPERATION|PROCESS_CREATE_THREAD|SYNCHRONIZE,FALSE,pid);
        if(!h)return 20;wchar_t path[32768];DWORD size=32768;
        if(!QueryFullProcessImageNameW(h,0,path,&size)||hashFile(path)!=L"1B7C8676E778C7BD47F55184F927338DA3CF70C6AA5F0BEB59869EA0F0AC9D4E"){CloseHandle(h);return 21;}
        HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPMODULE,pid);MODULEENTRY32W m{};m.dwSize=sizeof(m);U base=0;
        if(snap!=INVALID_HANDLE_VALUE){if(Module32FirstW(snap,&m))base=(U)m.modBaseAddr;CloseHandle(snap);}
        session.process=h;session.base=base;ModContext check;
        if(teamCLI||teamCreateCLI||teamAiCLI){if(!(teamAiCLI?startTeamController(serial):startTeamProbe(serial,teamCreateCLI))){CloseHandle(h);session={};return 24;}}
        else {if(!practiceContext(check,aiCLI)){CloseHandle(h);session={};return 24;}if(aiCLI)startPracticeAi();else startPracticeMod(restoreCLI);}
        if(!modJob.busy()){CloseHandle(h);session={};return 22;}
        int result=-1;ULONGLONG until=GetTickCount64()+30000;
        while(modJob.busy()&&GetTickCount64()<until){result=modJob.poll();Sleep(10);}
        bool pending=modJob.busy();CloseHandle(h);session={};return pending?23:result==3?0:30+result;
    }
    bool uiTest=!wcscmp(args,L"--ui-self-test");
    if(uiTest){wchar_t temp[MAX_PATH],file[MAX_PATH];if(!GetTempPathW(MAX_PATH,temp)||!GetTempFileNameW(temp,L"KKT",0,file))return 10;settingsPath=file;}
    SetProcessDPIAware();WNDCLASSW wc{};wc.lpfnWndProc=proc;wc.hInstance=instance;wc.lpszClassName=L"KKExternalTool";wc.hCursor=LoadCursor(nullptr,IDC_ARROW);wc.hbrBackground=(HBRUSH)(COLOR_WINDOW+1);RegisterClassW(&wc);
    font=CreateFontW(-18,0,0,0,FW_NORMAL,FALSE,FALSE,FALSE,DEFAULT_CHARSET,0,0,CLEARTYPE_QUALITY,0,L"Microsoft YaHei UI");
    window=CreateWindowW(wc.lpszClassName,L"功夫小子 · 独立调试工具",WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_MINIMIZEBOX,CW_USEDEFAULT,CW_USEDEFAULT,740,840,nullptr,nullptr,instance,nullptr);
    combo=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST|WS_VSCROLL|WS_TABSTOP,20,20,440,200);
    control(L"BUTTON",L"刷新",WS_TABSTOP,475,20,95,32,10);control(L"BUTTON",L"连接",WS_TABSTOP,585,20,110,32,11);
    control(L"STATIC",L"功能（自动记住勾选）",0,20,72,280,28);control(L"STATIC",L"设置值",0,325,72,120,28);control(L"STATIC",L"当前原始值",0,490,72,180,28);
    int y=110,i=0;for(auto& f:features){f.check=control(L"BUTTON",f.name,BS_AUTOCHECKBOX|WS_TABSTOP,20,y,300,30,100+i);f.edit=control(L"EDIT",f.initial,WS_BORDER|ES_AUTOHSCROLL|WS_TABSTOP,325,y,125,30,200+i++);if(f.isInt)EnableWindow(f.edit,FALSE);f.value=control(L"STATIC",L"—",0,490,y+3,180,26);y+=48;}
    status=control(L"STATIC",L"先选择游戏进程并连接；进入战斗后执行已勾选的功能。",0,20,410,680,68);
    killButton=control(L"BUTTON",L"自动秒杀敌方（每 400 毫秒）",BS_AUTOCHECKBOX|WS_TABSTOP,20,485,500,32,13);
    poisonCheck=control(L"BUTTON",L"NPC 中毒（1 级 / 3 秒，4 秒补一次）",BS_AUTOCHECKBOX|WS_TABSTOP,20,523,650,32,14);
    killStatus=control(L"STATIC",L"上一轮未结束则跳过；换 PID 后保留勾选。",0,20,568,680,52);
    control(L"STATIC",L"实验功能：纯 EXE，无 DLL；容错不能保证游戏绝不闪退。\r\n加速、自动反击、自动准备尚未移植。",0,20,622,680,48);
    control(L"BUTTON",L"停止并断开（保留勾选）",WS_TABSTOP,435,675,260,34,12);
    // Keep connection controls shared, with independent feature and MOD pages.
    for(HWND c=GetWindow(window,GW_CHILD);c;c=GetWindow(c,GW_HWNDNEXT)){
        RECT r{};GetWindowRect(c,&r);MapWindowPoints(nullptr,window,(POINT*)&r,2);
        if(r.top>=72&&GetDlgCtrlID(c)!=12){featureControls.push_back(c);SetWindowPos(c,nullptr,r.left,r.top+42,0,0,SWP_NOSIZE|SWP_NOZORDER);}
        if(GetDlgCtrlID(c)==12)SetWindowPos(c,nullptr,r.left,r.top+42,0,0,SWP_NOSIZE|SWP_NOZORDER);
    }
    INITCOMMONCONTROLSEX common{sizeof(common),ICC_TAB_CLASSES};InitCommonControlsEx(&common);
    tabs=control(WC_TABCONTROLW,L"",WS_TABSTOP,20,66,675,32,29);
    TCITEMW item{};item.mask=TCIF_TEXT;item.pszText=(LPWSTR)L"调试";TabCtrl_InsertItem(tabs,0,&item);item.pszText=(LPWSTR)L"MOD（实验）";TabCtrl_InsertItem(tabs,1,&item);
    modControls.push_back(control(L"STATIC",L"原版中立怪物 · 线下实验",0,24,120,650,32));
    monsterPreview=control(L"STATIC",L"",SS_BITMAP,24,162,140,140);modControls.push_back(monsterPreview);
    const U visibleModels[]={0,2}; // Preserve server template IDs: bear remains 2.
    for(U previewIndex=0;previewIndex<2;previewIndex++)monsterBitmaps[previewIndex]=(HBITMAP)LoadImageW(instance,MAKEINTRESOURCEW(301+visibleModels[previewIndex]),IMAGE_BITMAP,140,140,LR_CREATEDIBSECTION);
    modControls.push_back(control(L"STATIC",L"怪物（团队模式）",0,182,162,200,24));
    monsterChoice=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST|WS_VSCROLL|WS_TABSTOP,182,188,250,140,34);modControls.push_back(monsterChoice);
    for(auto name:{L"恶棍",L"熊"}){LRESULT index=SendMessageW(monsterChoice,CB_ADDSTRING,0,(LPARAM)name);SendMessageW(monsterChoice,CB_SETITEMDATA,index,visibleModels[index]);}SendMessageW(monsterChoice,CB_SETCURSEL,0,0);
    modControls.push_back(control(L"STATIC",L"数量",0,452,162,100,24));
    monsterCount=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST|WS_TABSTOP,452,188,160,140,35);modControls.push_back(monsterCount);
    for(auto name:{L"1 只",L"2 只",L"3 只",L"4 只"})SendMessageW(monsterCount,CB_ADDSTRING,0,(LPARAM)name);SendMessageW(monsterCount,CB_SETCURSEL,0,0);
    modControls.push_back(control(L"STATIC",L"AI 行为",0,182,234,100,24));
    monsterAI=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST|WS_TABSTOP,182,260,430,120,36);modControls.push_back(monsterAI);
    SendMessageW(monsterAI,CB_ADDSTRING,0,(LPARAM)L"原版（pve008）");SendMessageW(monsterAI,CB_ADDSTRING,0,(LPARAM)L"主动近战（pve006，实验）");SendMessageW(monsterAI,CB_SETCURSEL,1,0);refreshMonsterPreview();
    modControls.push_back(control(L"STATIC",L"团队：房主点一次，全员自动生成；每局一批，间隔 20 单位。\r\n所有窗口需在本机；先取消秒杀、中毒。训练仍为单只恶棍。",0,24,308,650,42));
    modControls.push_back(control(L"BUTTON",L"训练：生成恶棍",WS_TABSTOP,24,358,220,38,30));
    modControls.push_back(control(L"BUTTON",L"训练：启用 AI",WS_TABSTOP,264,358,220,38,31));
    modControls.push_back(control(L"BUTTON",L"请求全员生成（房主）",WS_TABSTOP,24,404,220,38,32));
    modControls.push_back(control(L"BUTTON",L"团队启用 AI（房主）",WS_TABSTOP,264,404,220,38,33));
    modStatus=control(L"EDIT",L"图片取自原版模型与贴图；怪物在房主附近分散生成。\r\n新怪物与多只同步仍需实测。",WS_BORDER|ES_READONLY|ES_MULTILINE|WS_VSCROLL,24,458,650,205);
    modControls.push_back(modStatus);selectTab(0);
    // Shared identity row remains visible on either tab.
    for(HWND c=GetWindow(window,GW_CHILD);c;c=GetWindow(c,GW_HWNDNEXT)){
        RECT r{};GetWindowRect(c,&r);MapWindowPoints(nullptr,window,(POINT*)&r,2);
        if(r.top>=66)SetWindowPos(c,nullptr,r.left,r.top+38,0,0,SWP_NOSIZE|SWP_NOZORDER);
    }
    identityLabel=control(L"STATIC",L"未连接 · 房主身份未知",0,20,66,675,30);
    loadSettings();
    if(uiTest) {
        for(auto& f:features)SendMessageW(f.check,BM_SETCHECK,BST_CHECKED,0);
        SendMessageW(killButton,BM_SETCHECK,BST_CHECKED,0);SendMessageW(poisonCheck,BM_SETCHECK,BST_CHECKED,0);SetWindowTextW(features[0].edit,L"321");saveSettings();
        bool ok=disconnect()&&monsterBitmaps[0]&&monsterBitmaps[1]&&SendMessageW(monsterChoice,CB_GETCOUNT,0,0)==2&&SendMessageW(monsterChoice,CB_GETITEMDATA,1,0)==2&&SendMessageW(monsterCount,CB_GETCOUNT,0,0)==4;for(auto& f:features)ok=ok&&checked(f.check);ok=ok&&checked(killButton)&&checked(poisonCheck);
        selectTab(1);ok=ok&&!(GetWindowLongW(features[0].check,GWL_STYLE)&WS_VISIBLE)&&(GetWindowLongW(modStatus,GWL_STYLE)&WS_VISIBLE);
        selectTab(0);ok=ok&&(GetWindowLongW(features[0].check,GWL_STYLE)&WS_VISIBLE)&&!(GetWindowLongW(modStatus,GWL_STYLE)&WS_VISIBLE);
        loadingSettings=true;for(auto& f:features)SendMessageW(f.check,BM_SETCHECK,BST_UNCHECKED,0);
        SendMessageW(killButton,BM_SETCHECK,BST_UNCHECKED,0);SendMessageW(poisonCheck,BM_SETCHECK,BST_UNCHECKED,0);SetWindowTextW(features[0].edit,L"0");
        loadSettings();for(auto& f:features)ok=ok&&checked(f.check);ok=ok&&checked(killButton)&&checked(poisonCheck);
        wchar_t value[80];GetWindowTextW(features[0].edit,value,80);ok=ok&&!wcscmp(value,L"321");
        // A PID switch/stop must keep preferences and wait for the active call.
        HANDLE self=nullptr;DuplicateHandle(GetCurrentProcess(),GetCurrentProcess(),GetCurrentProcess(),&self,0,FALSE,DUPLICATE_SAME_ACCESS);
        KillCall k;k.source=10;k.target=20;k.send=(U)&fakeSlowSend;k.damage=(U)&fakeDamage;k.bits=0x461C3C00;fakeExpected=k;
        session.process=self;autoPaused=false;killCancel=false;
        bool launched=self&&killJob.start(self,k);ok=ok&&launched;
        if(launched){ok=ok&&!disconnect()&&disconnectPending&&autoPaused&&checked(killButton)&&checked(poisonCheck);
            WaitForSingleObject(killJob.thread,3000);tick();ok=ok&&!session.process&&!killBusy()&&!disconnectPending&&checked(features[0].check);}
        else if(self){CloseHandle(self);session={};}
        // Execute generated dispatch code in our own hidden test window only.
        // Verify main-thread execution, stale guards and exception cleanup.
        session.process=GetCurrentProcess();
        for(auto name:{"GetWindowLongW","SetWindowLongW","CallWindowProcW"}){
            U target=remoteUser32(name);MEMORY_BASIC_INFORMATION mbi{};
            ok=ok&&target&&VirtualQuery((void*)target,&mbi,sizeof(mbi))&&mbi.AllocationBase==GetModuleHandleW(L"user32.dll");
        }
        ok=ok&&remoteUser32("OpenKFO_missing_export_test")==0;
        session={};
        for(int scenario=0;scenario<3;scenario++){
            session.process=GetCurrentProcess();U flag=0,guard=42;U old=(U)GetWindowLongW(window,GWL_WNDPROC);
            bool started=modJob.start({{(U)&guard,scenario==1?43U:42U}},[&](Code32& c,U data){
                if(scenario==2){c.bytes({0xA1});c.word(1);}
                c.call((U)&GetCurrentThreadId);c.bytes({0xA3});c.word((U)&flag);c.status(data,3);
            },window);ok=ok&&started;
            int result=-1;ULONGLONG until=GetTickCount64()+3000;
            while(started&&modJob.busy()&&GetTickCount64()<until){result=modJob.poll();Sleep(1);}
            ok=ok&&!modJob.busy()&&result==(scenario==0?3:scenario==1?4:6)&&flag==(scenario==0?GetCurrentThreadId():0U)&&(U)GetWindowLongW(window,GWL_WNDPROC)==old;
            session={};
        }
        DestroyWindow(window);DeleteObject(font);DeleteFileW(settingsPath.c_str());return ok?0:11;
    }
    refresh();SetTimer(window,1,100,nullptr);ShowWindow(window,show);
    MSG msg;while(GetMessageW(&msg,nullptr,0,0)>0){if(!IsDialogMessageW(window,&msg)){TranslateMessage(&msg);DispatchMessageW(&msg);}}
    DeleteObject(font);return 0;
}
