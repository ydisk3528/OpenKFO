#include <algorithm>
#define KK_COMPACT_DEBUG
#define wWinMain LegacyWinMain
#include "main.cpp"
#undef wWinMain

DWORD attachedPID=0; uint64_t attachedCreated=0;
HWND attachedWindow=nullptr, toggleButton=nullptr, spawnButton=nullptr, passageButton=nullptr;
bool passageRunning=false; U passageRoom=0,passageSerial=0; ULONGLONG lastPlanPoll=0;
bool expanded=false; int activeTab=0, npcJob=0;
U attemptedSerial=0, aiSerial=0;
BYTE snapshot[216]{};

const wchar_t* npcCompletionText(int job,int result) {
    if(result!=3)return L"操作失败或场景已变化；本局不再重试，请重新开局。";
    if(job==3)return L"怪物已生成，AI 已启动。";
    if(job==2)return L"本机怪物已生成，等待其他玩家生成确认。";
    return L"生成请求已发送，等待服务器下发任务。";
}

uint64_t nowMillis() {
    FILETIME f;GetSystemTimeAsFileTime(&f);
    return ((((uint64_t)f.dwHighDateTime<<32)|f.dwLowDateTime)-116444736000000000ULL)/10000;
}
bool readSnapshot() {
    HANDLE f=CreateFileW(overlayPlanFile.c_str(),GENERIC_READ,FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,nullptr,OPEN_EXISTING,0,nullptr);
    if(f==INVALID_HANDLE_VALUE)return false;
    BYTE data[216]{};DWORD n=0;bool ok=ReadFile(f,data,sizeof(data),&n,nullptr)&&n==sizeof(data);CloseHandle(f);
    uint64_t time=0;memcpy(&time,data,8);
    if(!ok||nowMillis()<time||nowMillis()-time>3500)return false;
    memcpy(snapshot,data,sizeof(data));return true;
}
bool snapshotContext() {
    Actor own;U manager=0,room=0,serial=0,scene=0,phase=0;
    uint64_t uid=0;memcpy(&uid,snapshot+8,8);
    TeamPlan p;memcpy(&p,snapshot+16,192);
    return session.current(own)&&own.uid==uid&&
        read(session.process,session.base+0x13C8708,manager)&&
        read(session.process,manager+0x311,room)&&read(session.process,manager+0x315,serial)&&
        p.room==room&&p.serial==serial&&room&&serial&&
        read(session.process,session.base+0x13C8710,scene)&&read(session.process,scene+0x78,phase)&&phase==4;
}
bool passageContext(bool starting) {
    Actor own;U manager=0,room=0,serial=0,scene=0,phase=0,mode=0,vt=0;
    if(!session.current(own)||!own.state||
       !read(session.process,session.base+0x13C8708,manager)||
       !read(session.process,manager+0x311,room)||!room||
       !read(session.process,manager+0x315,serial)||!serial||
       !read(session.process,session.base+0x13C8710,scene)||
       !read(session.process,scene+0x78,phase)||phase!=4)return false;
    if(!starting)return room==passageRoom&&serial==passageSerial;
    bool native=read(session.process,scene+0xAC,mode)&&read(session.process,mode,vt)&&vt==session.base+0x7AD584;
    if(!native&&!(readSnapshot()&&snapshotContext()&&snapshot[208]==1))return false;
    passageRoom=room;passageSerial=serial;return true;
}
void stopPassage() {
    passageRunning=false;autoPaused=true;killCancel=true;
    restoreFeatures();pollKill();
    SetWindowTextW(passageButton,L"启动");
}
bool overlayPveContext() {
    if(!passageRunning)return false;
    if(passageContext(false))return true;
    // The caller cancels the queued work; do not re-enter pollKill here.
    passageRunning=false;autoPaused=true;killCancel=true;
    SetWindowTextW(passageButton,L"启动");return false;
}
bool overlayReadPlan(void* output,DWORD length) {
    if(length!=192||!readSnapshot()||!snapshotContext()||snapshot[212]!=1)return false;
    memcpy(output,snapshot+16,192);return true;
}
BOOL CALLBACK findGame(HWND h,LPARAM) {
    DWORD pid=0;GetWindowThreadProcessId(h,&pid);
    if(pid==attachedPID&&IsWindowVisible(h)&&!GetWindow(h,GW_OWNER)){attachedWindow=h;return FALSE;}
    return TRUE;
}
void layoutOverlay() {
    for(HWND h:featureControls)ShowWindow(h,expanded&&activeTab==1?SW_SHOW:SW_HIDE);
    for(HWND h:modControls)ShowWindow(h,expanded&&activeTab==0?SW_SHOW:SW_HIDE);
    ShowWindow(tabs,expanded?SW_SHOW:SW_HIDE);
    SetWindowTextW(toggleButton,expanded?L"MOD ▴":L"MOD ▾");
}
void updateOverlay() {
    if(!session.process||WaitForSingleObject(session.process,0)!=WAIT_TIMEOUT){PostMessageW(window,WM_CLOSE,0,0);return;}
    if(passageRunning){
        if(!overlayPveContext())stopPassage();
        else if(!npcJob&&!modJob.busy()){autoPaused=false;tick();}
    }else if(killBusy()){killCancel=true;pollKill();}
    attachedWindow=nullptr;EnumWindows(findGame,0);
    if(!attachedWindow||IsIconic(attachedWindow)){ShowWindow(window,SW_HIDE);return;}
    SetWindowLongW(window,GWL_HWNDPARENT,(LONG)attachedWindow);
    RECT rect;GetClientRect(attachedWindow,&rect);POINT origin{};ClientToScreen(attachedWindow,&origin);
    int width=expanded?420:90;
    int x=origin.x+std::min(std::max(0L,(rect.right/2)+90),std::max(0L,rect.right-width));
    SetWindowPos(window,HWND_TOP,x,origin.y+8,width,expanded?285:30,SWP_NOACTIVATE|SWP_SHOWWINDOW);
    if(closePending){if(disconnect())DestroyWindow(window);return;}
    if(modJob.busy()) {
        int result=modJob.poll();
        if(result>=3){
            SetWindowTextW(modStatus,npcCompletionText(npcJob,result));
            npcJob=0;teamAuto.clear();
        }
        return;
    }
    // Idle UI does not inspect game mode. Only incoming server work needs
    // execution-time validation; non-host peers must still receive spawn tasks.
    auto now=GetTickCount64();if(now-lastPlanPoll<1000)return;lastPlanPoll=now;
    if(!readSnapshot())return;
    TeamPlan p;memcpy(&p,snapshot+16,192);
    if(p.state!=1&&p.state!=2&&p.state!=3)return;
    uint64_t uid=0;memcpy(&uid,snapshot+8,8);
    bool team=snapshot[212]==1&&snapshotContext()&&validTeamPlan(p,p.room,p.serial);
    bool owner=team&&p.owner==uid;
    if(team&&p.state==1&&attemptedSerial!=p.serial&&!killBusy()) {
        for(U i=0;i<p.count;i++)if(p.members[i].uid==uid&&p.members[i].state==1) {
            attemptedSerial=p.serial;
            if(startTeamProbe(p.serial,true)){npcJob=2;SetWindowTextW(modStatus,L"收到服务器任务，正在生成本机怪物…");}
            else SetWindowTextW(modStatus,L"生成条件不满足，等待本次任务取消；请重新开局。");
            break;
        }
    } else if(team&&p.state==2&&owner&&aiSerial!=p.serial&&!killBusy()) {
        aiSerial=p.serial;
        if(startTeamController(p.serial)){npcJob=3;SetWindowTextW(modStatus,L"全员生成已确认，房主正在开启 AI…");}
        else SetWindowTextW(modStatus,L"AI 启动失败，请重新开局。");
    } else if(team&&p.state==3)SetWindowTextW(modStatus,L"本次生成已取消（超时、玩家退出或生成失败），请重新开局。");
}
LRESULT CALLBACK overlayProc(HWND h,UINT msg,WPARAM w,LPARAM l) {
    if(msg==WM_TIMER){updateOverlay();return 0;}
    if(msg==WM_NOTIFY&&((NMHDR*)l)->hwndFrom==tabs&&((NMHDR*)l)->code==TCN_SELCHANGE){activeTab=TabCtrl_GetCurSel(tabs);layoutOverlay();return 0;}
    if(msg==WM_COMMAND) {
        if(LOWORD(w)==70){expanded=!expanded;layoutOverlay();updateOverlay();}
        if(LOWORD(w)==71){
            if(passageRunning)stopPassage();
            else if(!modJob.busy()&&!killBusy()&&passageContext(true)){
                passageRunning=true;autoFault=attributeFault=false;autoPaused=false;
                SetWindowTextW(passageButton,L"已启动");
            }else SetWindowTextW(passageButton,L"启动");
        }
        if(LOWORD(w)==32&&!modJob.busy()&&!killBusy()) {
            if(requestTeamSpawn()){npcJob=1;SetWindowTextW(modStatus,L"生成请求已发送，等待全员确认…");}
            else SetWindowTextW(modStatus,L"未生成：请确认房主身份、本局未生成且服务器已开启功能。");
        }
        return 0;
    }
    if(msg==WM_CLOSE){closePending=true;if(disconnect())DestroyWindow(h);return 0;}
    if(msg==WM_DESTROY){PostQuitMessage(0);return 0;}
    return DefWindowProcW(h,msg,w,l);
}
int WINAPI wWinMain(HINSTANCE instance,HINSTANCE previous,LPWSTR args,int show) {
    if(!wcscmp(args,L"--status-self-test"))return
        !wcscmp(npcCompletionText(3,3),L"怪物已生成，AI 已启动。")&&
        !wcscmp(npcCompletionText(2,3),L"本机怪物已生成，等待其他玩家生成确认。")&&
        !wcscmp(npcCompletionText(1,3),L"生成请求已发送，等待服务器下发任务。")&&
        !wcscmp(npcCompletionText(3,5),L"操作失败或场景已变化；本局不再重试，请重新开局。")?0:14;
    if(!wcscmp(args,L"--self-test")||!wcscmp(args,L"--ui-self-test"))return LegacyWinMain(instance,previous,args,show);
    bool uiTest=!wcscmp(args,L"--overlay-ui-test");
    if(uiTest){FILETIME a,b,c,d;GetProcessTimes(GetCurrentProcess(),&a,&b,&c,&d);attachedPID=GetCurrentProcessId();attachedCreated=((uint64_t)a.dwHighDateTime<<32)|a.dwLowDateTime;}
    bool inspect=swscanf_s(args,L"--inspect %lu %llu",&attachedPID,&attachedCreated)==2;
    if(!uiTest&&!inspect&&swscanf_s(args,L"--attach %lu %llu",&attachedPID,&attachedCreated)!=2)return 2;
    HANDLE process=OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION,FALSE,attachedPID);
    FILETIME created,exit,kernel,user;
    bool same=process&&GetProcessTimes(process,&created,&exit,&kernel,&user)&&((((uint64_t)created.dwHighDateTime<<32)|created.dwLowDateTime)==attachedCreated);
    if(process)CloseHandle(process);if(!same)return 3;
    wchar_t name[128];swprintf_s(name,L"Local\\OpenKFO-GameMod-%lu-%llu",attachedPID,attachedCreated);
    HANDLE singleton=inspect?nullptr:CreateMutexW(nullptr,FALSE,name);if(!inspect&&(!singleton||GetLastError()==ERROR_ALREADY_EXISTS))return 0;
    wchar_t local[32768];DWORD n=GetEnvironmentVariableW(L"LOCALAPPDATA",local,32768);if(!n||n>=32768)return 4;
    wchar_t file[128];swprintf_s(file,L"\\OpenKFO\\mod\\%lu-%llu.bin",attachedPID,attachedCreated);overlayPlanFile=std::wstring(local)+file;
    if(inspect){
        session.process=OpenProcess(PROCESS_VM_READ|PROCESS_QUERY_INFORMATION|SYNCHRONIZE,FALSE,attachedPID);
        session.base=0x400000;
        bool fresh=readSnapshot(),context=fresh&&snapshotContext();
        printf("fresh=%d context=%d pve=%u path=%ls\n",fresh,context,snapshot[208],overlayPlanFile.c_str());
        if(session.process)CloseHandle(session.process);return context?0:13;
    }
    SetProcessDPIAware();INITCOMMONCONTROLSEX common{sizeof(common),ICC_TAB_CLASSES};InitCommonControlsEx(&common);
    WNDCLASSW wc{};wc.lpfnWndProc=overlayProc;wc.hInstance=instance;wc.lpszClassName=L"OpenKFOGameMod";wc.hCursor=LoadCursor(nullptr,IDC_ARROW);wc.hbrBackground=(HBRUSH)(COLOR_WINDOW+1);RegisterClassW(&wc);
    font=CreateFontW(-16,0,0,0,FW_NORMAL,FALSE,FALSE,FALSE,DEFAULT_CHARSET,0,0,CLEARTYPE_QUALITY,0,L"Microsoft YaHei UI");
    window=CreateWindowExW(WS_EX_TOOLWINDOW,wc.lpszClassName,L"MOD",WS_POPUP,0,0,90,30,nullptr,nullptr,instance,nullptr);
    toggleButton=control(L"BUTTON",L"MOD ▾",BS_PUSHBUTTON,0,0,90,30,70);
    tabs=control(WC_TABCONTROLW,L"",0,8,36,400,30);TCITEMW tab{};tab.mask=TCIF_TEXT;
    tab.pszText=(LPWSTR)L"怪物 NPC 生成";TabCtrl_InsertItem(tabs,0,&tab);tab.pszText=(LPWSTR)L"过关专属";TabCtrl_InsertItem(tabs,1,&tab);
    identityLabel=control(L"STATIC",L"团队模式 才启用",0,14,80,390,40);
    monsterChoice=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST,14,117,190,150,34);
    for(auto name:{L"恶棍",L"熊",L"随机怪物"})SendMessageW(monsterChoice,CB_ADDSTRING,0,(LPARAM)name);
    SendMessageW(monsterChoice,CB_SETITEMDATA,0,0);SendMessageW(monsterChoice,CB_SETITEMDATA,1,2);SendMessageW(monsterChoice,CB_SETITEMDATA,2,3);SendMessageW(monsterChoice,CB_SETCURSEL,0,0);
    monsterCount=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST,214,117,190,150,35);
    for(auto name:{L"1 只",L"2 只",L"3 只",L"4 只",L"随机数量（1～4）"})SendMessageW(monsterCount,CB_ADDSTRING,0,(LPARAM)name);
    SendMessageW(monsterCount,CB_SETCURSEL,0,0);
    spawnButton=control(L"BUTTON",L"团队生成",BS_PUSHBUTTON,14,154,160,34,32);
    modStatus=control(L"STATIC",L"房主触发，全员自动接收；全部确认后开启 AI。",0,14,196,385,74);
    modControls={identityLabel,monsterChoice,monsterCount,spawnButton,modStatus};
    killButton=control(L"BUTTON",L"秒杀 NPC",BS_AUTOCHECKBOX,14,80,360,30,13);
    poisonCheck=control(L"BUTTON",L"NPC 中毒",BS_AUTOCHECKBOX,14,114,360,30,14);
    features[0].check=control(L"BUTTON",L"无限怒气",BS_AUTOCHECKBOX,14,148,360,30,15);
    // Legacy workers still report status through these handles; keep them hidden.
    status=control(L"STATIC",L"",0,0,0,1,1);
    killStatus=control(L"STATIC",L"",0,0,0,1,1);
    ShowWindow(status,SW_HIDE);ShowWindow(killStatus,SW_HIDE);
    passageButton=control(L"BUTTON",L"启动",BS_PUSHBUTTON,14,190,160,34,71);
    featureControls={killButton,poisonCheck,features[0].check,passageButton};
    combo=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST,0,0,1,1);SendMessageW(combo,CB_ADDSTRING,0,(LPARAM)L"attached");SendMessageW(combo,CB_SETITEMDATA,0,attachedPID);SendMessageW(combo,CB_SETCURSEL,0,0);ShowWindow(combo,SW_HIDE);
    for(HWND* h:{&monsterAI}){*h=control(L"COMBOBOX",L"",CBS_DROPDOWNLIST,0,0,1,1);SendMessageW(*h,CB_ADDSTRING,0,(LPARAM)L"default");SendMessageW(*h,CB_SETCURSEL,0,0);ShowWindow(*h,SW_HIDE);}
    SendMessageW(monsterChoice,CB_SETITEMDATA,0,0); // One default monster; no extra NPC controls.
    SendMessageW(monsterAI,CB_ADDSTRING,0,(LPARAM)L"active");SendMessageW(monsterAI,CB_SETCURSEL,1,0);
    if(uiTest){
        layoutOverlay();bool ok=!(GetWindowLongW(tabs,GWL_STYLE)&WS_VISIBLE);
        expanded=true;activeTab=0;layoutOverlay();ok=ok&&(GetWindowLongW(modStatus,GWL_STYLE)&WS_VISIBLE)&&!(GetWindowLongW(killButton,GWL_STYLE)&WS_VISIBLE);
        activeTab=1;layoutOverlay();ok=ok&&!(GetWindowLongW(modStatus,GWL_STYLE)&WS_VISIBLE)&&(GetWindowLongW(killButton,GWL_STYLE)&WS_VISIBLE)&&TabCtrl_GetItemCount(tabs)==2&&!overlayPveContext()&&!checked(killButton)&&!checked(poisonCheck)&&!checked(features[0].check);
        SendMessageW(window,WM_COMMAND,71,0);ok=ok&&!passageRunning;
        passageRunning=true;passageRoom=1;passageSerial=1;
        ok=ok&&!overlayPveContext()&&!passageRunning;
        wchar_t label[32];GetWindowTextW(passageButton,label,32);ok=ok&&!wcscmp(label,L"启动");
        DestroyWindow(window);DeleteObject(font);CloseHandle(singleton);return ok?0:12;
    }
    overlayNpcOnly=true;connect();layoutOverlay();SetTimer(window,1,100,nullptr);
    MSG msg;while(GetMessageW(&msg,nullptr,0,0)>0){TranslateMessage(&msg);DispatchMessageW(&msg);}
    DeleteObject(font);CloseHandle(singleton);return 0;
}
