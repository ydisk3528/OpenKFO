HWND identityLabel=nullptr;
DWORD identityUpdated=0;
// gfxz 1B7C8676 only: native 44B3F0 reads actor+1B74.
// 2026-09-24: both clients agreed before and after swapping test001/test002
// as team room owner; local server owner UID independently matched each round.
void updateIdentity(bool force=false) {
    if(!identityLabel)return;
    DWORD now=GetTickCount();if(!force&&now-identityUpdated<500)return;identityUpdated=now;
    std::wstring label=L"未连接 · 房主身份未知";
    if(session.process) {
        DWORD pid=GetProcessId(session.process);wchar_t text[220];
        swprintf_s(text,L"PID %lu · 未在团队战地图内，房主身份未知",pid);label=text;
        Actor own;U scene=0,phase=0,mode=0,vt=0,manager=0,flag=0,again=0;uint64_t round=0,check=0;
        if(session.current(own)&&read(session.process,session.base+0x13C8710,scene)&&
           read(session.process,scene+0x78,phase)&&phase==4&&
           read(session.process,scene+0xAC,mode)&&read(session.process,mode,vt)&&
           (vt==session.base+0x7A9144||vt==session.base+0x7A87D4)&&
           read(session.process,session.base+0x13C8708,manager)&&read(session.process,manager+0x311,round)&&round&&
           read(session.process,own.ptr+0x1B74,flag)&&flag<=1&&
           read(session.process,manager+0x311,check)&&check==round&&
           read(session.process,session.base+0x13C8710,again)&&again==scene&&
           read(session.process,scene+0x78,phase)&&phase==4) {
            swprintf_s(text,L"PID %lu · UID %llu · %s",pid,(unsigned long long)own.uid,
                flag?L"房主 / 战斗主控":L"队员（非房主）");label=text;
        }
    }
    wchar_t old[256];GetWindowTextW(identityLabel,old,256);
    if(label!=old)SetWindowTextW(identityLabel,label.c_str());
}
