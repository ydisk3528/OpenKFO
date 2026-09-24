// Loopback-only coordinator: the server owns the plan; each child EXE validates
// that plan for its own UID before creating a passive replica. No AI auto-start.
std::wstring hashFile(const wchar_t* path);
struct TeamWorker {uint64_t uid=0;DWORD pid=0;HANDLE process=nullptr;DWORD exitCode=STILL_ACTIVE;};
struct TeamAuto {
    bool requesting=false,watching=false,launched=false;
    U room=0,serial=0;uint64_t owner=0;ULONGLONG started=0,lastPoll=0;
    HANDLE claim=nullptr;
    std::vector<TeamWorker> workers;
    bool busy()const{return requesting||watching;}
    void clear(){for(auto& w:workers)if(w.process)CloseHandle(w.process);workers.clear();if(claim)CloseHandle(claim);claim=nullptr;requesting=watching=launched=false;}
} teamAuto;

bool clientIdentity(DWORD pid,U room,U serial,uint64_t& uid) {
    HANDLE h=OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|PROCESS_VM_READ,FALSE,pid);if(!h)return false;
    wchar_t path[32768];DWORD size=32768;bool ok=false;
    if(QueryFullProcessImageNameW(h,0,path,&size)&&hashFile(path)==L"1B7C8676E778C7BD47F55184F927338DA3CF70C6AA5F0BEB59869EA0F0AC9D4E"){
        HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPMODULE,pid);MODULEENTRY32W m{};m.dwSize=sizeof(m);U base=0;
        if(snap!=INVALID_HANDLE_VALUE){if(Module32FirstW(snap,&m))base=(U)m.modBaseAddr;CloseHandle(snap);}
        U info=0,manager=0,scene=0,phase=0,r=0,s=0;
        ok=base&&read(h,base+0x13C86FC,info)&&read(h,info+0x18,uid)&&
            read(h,base+0x13C8708,manager)&&read(h,manager+0x311,r)&&read(h,manager+0x315,s)&&r==room&&s==serial&&
            read(h,base+0x13C8710,scene)&&read(h,scene+0x78,phase)&&phase==4;
    }
    CloseHandle(h);return ok;
}
bool findTeamWindows(const TeamPlan& p,std::vector<TeamWorker>& workers) {
    workers.clear();for(U i=0;i<p.count;i++)workers.push_back({p.members[i].uid});
    HANDLE snap=CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0);if(snap==INVALID_HANDLE_VALUE)return false;
    PROCESSENTRY32W e{};e.dwSize=sizeof(e);bool ok=true;
    if(Process32FirstW(snap,&e))do{
        if(_wcsicmp(e.szExeFile,L"gfxz.dat"))continue;
        uint64_t uid=0;if(!clientIdentity(e.th32ProcessID,p.room,p.serial,uid))continue;
        for(auto& w:workers)if(w.uid==uid){if(w.pid)ok=false;w.pid=e.th32ProcessID;}
    }while(Process32NextW(snap,&e));
    CloseHandle(snap);for(auto w:workers)if(!w.pid)ok=false;return ok;
}
bool requestTeamSpawn() {
    if(teamAuto.busy())return false;
    LRESULT selected=SendMessageW(monsterChoice,CB_GETCURSEL,0,0),count=SendMessageW(monsterCount,CB_GETCURSEL,0,0),ai=SendMessageW(monsterAI,CB_GETCURSEL,0,0);
    if(selected<0||selected>1||count<0||count>3||ai<0||ai>1)return false;
    U chosenModel=(U)SendMessageW(monsterChoice,CB_GETITEMDATA,selected,0),chosenCount=(U)count+1,chosenAI=(U)ai;
    if(chosenModel!=0&&chosenModel!=2)return false;
    Actor own;U manager=0,scene=0,phase=0,room=0,serial=0,floor=0,bits[3]{},send=0;
    if(!session.current(own)||!read(session.process,session.base+0x13C8708,manager)||
       !read(session.process,manager+0x311,room)||!read(session.process,manager+0x315,serial)||
       !read(session.process,session.base+0x13C8710,scene)||!read(session.process,scene+0x78,phase)||phase!=4||
       !read(session.process,own.ptr+0xD00,floor)||!read(session.process,session.base+0x63FBB0,send)||send!=0x51EC8B55)return false;
    TeamPlan p;if(!getTeamPlan(room,serial,p)||p.owner!=own.uid||p.state)return false;
    for(U i=0;i<3;i++){if(!read(session.process,own.ptr+0xD10+i*4,bits[i]))return false;float v;memcpy(&v,&bits[i],4);if(!std::isfinite(v)||fabs(v)>100000)return false;}
    wchar_t claimName[120];swprintf_s(claimName,L"Local\\OpenKFO-TeamPlan-%u-%u",room,serial);
    HANDLE claim=CreateMutexW(nullptr,FALSE,claimName);DWORD claimError=GetLastError();
    if(!claim||claimError==ERROR_ALREADY_EXISTS){if(claim)CloseHandle(claim);return false;}
    U base=session.base;
    std::vector<GuardWord> guards={{base+0x13C8710,scene},{scene+0x78,4},{base+0x13C8708,manager},
        {manager+0x311,room},{manager+0x315,serial},{own.ptr+0xC90,(U)own.uid},{own.ptr+0xC94,(U)(own.uid>>32)},{base+0x63FBB0,send}};
    bool ok=modJob.start(guards,[=](Code32& c,U data){
        c.status(data+128+39,100);c.status(data+128+43,room);c.status(data+128+47,serial);c.status(data+128+51,floor);
        for(U i=0;i<3;i++)c.status(data+128+55+i*4,bits[i]);
        c.status(data+128+67,chosenCount);c.status(data+128+71,chosenModel);c.status(data+128+75,chosenAI);
        c.push(0);c.push(0);c.push(79);c.push(data+128);c.push(21901);c.call(base+0x63FBB0);c.bytes({0x83,0xC4,0x14});c.status(data,3);
    });
    if(ok){teamAuto.clear();teamAuto.claim=claim;teamAuto.requesting=true;teamAuto.room=room;teamAuto.serial=serial;teamAuto.owner=own.uid;teamAuto.started=GetTickCount64();teamAuto.lastPoll=0;}else CloseHandle(claim);
    return ok;
}
void finishTeamRequest(int result) {
    teamAuto.requesting=false;
    teamAuto.watching=result==3;
    if(result!=3)teamAuto.clear();
    SetWindowTextW(modStatus,result==3?L"请求已发送，等待服务器创建任务…":L"生成请求未完成；未启动其他窗口生成。");
}
void cancelTeamAuto() {
    if(teamAuto.busy())reportTeamFailure(teamAuto.room,teamAuto.serial,teamAuto.owner,3);
    // Children may be inside a game's window callback. Never terminate them.
    teamAuto.clear();
}
const wchar_t* replicaError(U code) {
    switch(code){case 1:return L"找不到唯一的本机游戏窗口";case 2:return L"生成失败";case 3:return L"工具取消";case 4:return L"等待确认超时";case 5:return L"玩家已退出";default:return L"未知原因";}
}
void tickTeamAuto() {
    if(!teamAuto.watching||GetTickCount64()-teamAuto.lastPoll<1000)return;
    teamAuto.lastPoll=GetTickCount64();TeamPlan p;
    if(!getTeamPlan(teamAuto.room,teamAuto.serial,p)||!p.state){
        if(GetTickCount64()-teamAuto.started>50000){cancelTeamAuto();SetWindowTextW(modStatus,L"未能取得服务器任务状态，已停止。请确认实验服务器和本局状态。");}
        return;
    }
    if(p.state==1&&!teamAuto.launched){
        if(!findTeamWindows(p,teamAuto.workers)){
            uint64_t missing=p.owner;for(auto w:teamAuto.workers)if(!w.pid){missing=w.uid;break;}
            reportTeamFailure(p.room,p.serial,missing,1);teamAuto.launched=true;
            SetWindowTextW(modStatus,L"参战窗口不完整或重复：已取消生成。当前版本仅支持所有玩家在本机的线下测试。");return;
        }
        // Preflight all participants before launching any worker.
        teamAuto.launched=true;wchar_t exe[32768];DWORD n=GetModuleFileNameW(nullptr,exe,32768);
        for(auto& w:teamAuto.workers){
            wchar_t args[33000];swprintf_s(args,L"\"%s\" --team-create %lu %u",exe,w.pid,p.serial);
            STARTUPINFOW si{};si.cb=sizeof(si);si.dwFlags=STARTF_USESHOWWINDOW;si.wShowWindow=SW_HIDE;PROCESS_INFORMATION pi{};
            if(!n||n>=32768||!CreateProcessW(exe,args,nullptr,nullptr,FALSE,CREATE_NO_WINDOW,nullptr,nullptr,&si,&pi)){
                reportTeamFailure(p.room,p.serial,w.uid,2);w.exitCode=GetLastError();break;
            }
            w.process=pi.hProcess;CloseHandle(pi.hThread);
        }
    }
    for(auto& w:teamAuto.workers)if(w.process&&w.exitCode==STILL_ACTIVE&&WaitForSingleObject(w.process,0)==WAIT_OBJECT_0){
        GetExitCodeProcess(w.process,&w.exitCode);
        if(w.exitCode&&p.state==1)reportTeamFailure(p.room,p.serial,w.uid,2);
    }
    wchar_t summary[120];swprintf_s(summary,L"本局 %u 只怪物 · ID 100–%u · %s\r\n",p.monsters,99+p.monsters,p.ai?L"主动近战 AI":L"原版 AI");
    std::wstring text=summary;text+=p.state==2?L"所有窗口已确认生成。AI 尚未开启；请先检查双方可见。\r\n":p.state==3?
        L"本次生成已取消，禁止启动 AI。已出现的被动模型随本局结束清理。\r\n":L"服务器已下发任务，正在等待各窗口确认…\r\n";
    for(U i=0;i<p.count;i++){
        auto m=p.members[i];DWORD pid=0;for(auto w:teamAuto.workers)if(w.uid==m.uid)pid=w.pid;
        wchar_t row[200];swprintf_s(row,L"UID %llu · PID %lu · %s%s\r\n",(unsigned long long)m.uid,pid,
            m.state==2?L"已生成":m.state==3?L"失败：":L"等待确认",m.state==3?replicaError(m.error):L"");text+=row;
    }
    SetWindowTextW(modStatus,text.c_str());
    bool working=false;for(auto w:teamAuto.workers)if(w.process&&WaitForSingleObject(w.process,0)==WAIT_TIMEOUT)working=true;
    if(p.state!=1&&!working)teamAuto.clear();
}

bool teamPlanSelfTest() {
    TeamPlan p;p.magic=0x3343504e;p.serial=7;p.owner=1001;p.room=1;p.mode=1;p.npc=100;p.state=1;p.count=2;p.monsters=4;
    p.members[0]={1001,1,0};p.members[1]={1002,1,0};
    if(!validTeamPlan(p,1,7)||validTeamPlan(p,1,8))return false;
    p.members[1].uid=1001;if(validTeamPlan(p,1,7))return false;p.members[1].uid=1002;
    p.position[0]=0x7fc00000;if(validTeamPlan(p,1,7))return false;p.position[0]=0;
    p.monsters=5;if(validTeamPlan(p,1,7))return false;p.monsters=4;
    p.model=3;if(validTeamPlan(p,1,7))return false;p.model=0;
    p.ai=2;if(validTeamPlan(p,1,7))return false;p.ai=0;
    p.count=9;return !validTeamPlan(p,1,7);
}
