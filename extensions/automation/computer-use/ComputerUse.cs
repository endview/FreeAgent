// Windows-only optional extension. No Provider, model loop, or Core database.
using System;
using System.Collections;
using System.Collections.Generic;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Automation;
using System.Windows.Forms;

namespace FreeAgentAutomation {
class ComputerUse {
 static readonly JavaScriptSerializer Json = new JavaScriptSerializer { MaxJsonLength = 12*1024*1024, RecursionLimit=64 };
 static readonly object Gate = new object();
 static readonly ConcurrentDictionary<string, Snapshot> Views = new ConcurrentDictionary<string, Snapshot>();
 static string[] Allowed = new string[0];
 static volatile bool Paused = true;
 static volatile bool Stopping;
 static ControlHud Hud;
 static uint LastTick;
 static string ActiveScope="";
 static IntPtr Target=IntPtr.Zero;
 static readonly HashSet<string> Denied = new HashSet<string>(StringComparer.OrdinalIgnoreCase) {"powershell","pwsh","cmd","windowsterminal","conhost","wscript","cscript","regedit","mmc","taskmgr","lockapp","credentialuibroker","consent","sechealthui","keepass","1password","bitwarden","codex","chatgpt"};
 class Snapshot { public IntPtr Hwnd; public int PID; public long Start; public RECT Rect; public uint Dpi; public Dictionary<string,AutomationElement> Elements=new Dictionary<string,AutomationElement>(); public DateTime Time; public string Scope; public int Width,Height; }
 class Fault:Exception { public bool Unknown; public Fault(string m,bool u=false):base(m){Unknown=u;} }
 static string S(IDictionary<string,object> d,string k,string def=""){object o;return d!=null&&d.TryGetValue(k,out o)&&o!=null?Convert.ToString(o):def;}
 static bool B(IDictionary<string,object>d,string k){object o;return d!=null&&d.TryGetValue(k,out o)&&o is bool&&(bool)o;}
 static int I(IDictionary<string,object>d,string k,int v=0){object o;return d!=null&&d.TryGetValue(k,out o)?Convert.ToInt32(o):v;}
 static Dictionary<string,object>D(params object[] a){var d=new Dictionary<string,object>();for(int i=0;i<a.Length;i+=2)d[(string)a[i]]=a[i+1];return d;}
 static object Result(object value){return D("content",Json.Serialize(value));}
 static string Scope(IDictionary<string,object>d){foreach(var k in new[]{"core_id","space_id","conversation_id","run_id"})if(S(d,k)=="")throw new Fault("Missing trusted host scope; update FreeAgent");if(!B(d,"local"))throw new Fault("Computer Use accepts local Owner requests only");return S(d,"core_id")+"/"+S(d,"space_id")+"/"+S(d,"conversation_id");}
 static Dictionary<string,object>Obj(object o){return o as Dictionary<string,object>??new Dictionary<string,object>();}

 [STAThread] public static int Main(string[] args){
  try{SetProcessDpiAwarenessContext(new IntPtr(-4));}catch{}
  if(args.Length>0&&args[0]=="--fixture"){Application.EnableVisualStyles();Application.Run(new TestFixture(args));return 0;}
  Console.InputEncoding=new UTF8Encoding(false);Console.OutputEncoding=new UTF8Encoding(false);
  string line;bool initialized=false;
  try{while((line=Console.ReadLine())!=null){if(line.Length>12*1024*1024)throw new Fault("RPC frame too large");var request=Obj(Json.DeserializeObject(line));string method=S(request,"method");object id=request.ContainsKey("id")?request["id"]:null;var p=Obj(request.ContainsKey("params")?request["params"]:null);
   if(method=="$/cancel"){Paused=true;ShowHud();continue;}
   try{object answer;
    if(method=="initialize"){
     var caps=p.ContainsKey("capabilities")?p["capabilities"] as object[]:null;
     if(caps==null||!caps.Contains("tool_scope")||!caps.Contains("tool_artifacts"))throw new Fault("Requires FreeAgent v8.9.0-dev.2 tool_scope and tool_artifacts");
     if(initialized)throw new Fault("Already initialized");var settings=Obj(p.ContainsKey("settings")?p["settings"]:null);
     var raw=settings.ContainsKey("allowed_executables")?settings["allowed_executables"] as object[]:null;
     if(raw!=null)Allowed=raw.Select(x=>Path.GetFullPath(Convert.ToString(x))).ToArray();
     if(raw!=null&&raw.Any(x=>!Path.IsPathRooted(Convert.ToString(x)))||Allowed.Any(x=>!File.Exists(x)))throw new Fault("allowed_executables must contain existing absolute executable paths");
     initialized=true;answer=D("id","cc.endview.computer-use","version","0.1.0","provides",new[]{"tools"});
    }else if(method=="tools/list"){if(!initialized)throw new Fault("Not initialized");answer=D("tools",Tools());}
    else if(method=="shutdown"){Stopping=true;Paused=true;StopHud();Write(id,D());return 0;}
    else if(method=="tool.call"){
     if(!initialized)throw new Fault("Not initialized");var scope=Obj(p.ContainsKey("scope")?p["scope"]:null);var key=Scope(scope);
     if(ActiveScope!=""&&ActiveScope!=key){Paused=true;Views.Clear();Target=IntPtr.Zero;}ActiveScope=key;
     answer=Call(S(p,"name"),Obj(p.ContainsKey("arguments")?p["arguments"]:null),key,B(scope,"vision"));
    }else throw new Fault("Method unavailable");Write(id,answer);
   }catch(Fault e){if(e.Unknown)Error(id,-32006,e.Message);else Write(id,D("content",e.Message,"is_error",true));}
   catch(Exception){Write(id,D("content","Desktop operation failed. Observe again before repeating; no success is inferred.","is_error",true));Paused=true;ShowHud();}
  }}finally{Stopping=true;Paused=true;StopHud();}
  return 0;
 }
 static void Write(object id,object answer){lock(Gate){Console.WriteLine(Json.Serialize(D("jsonrpc","2.0","id",id,"result",answer)));Console.Out.Flush();}}
 static void Error(object id,int code,string m){lock(Gate){Console.WriteLine(Json.Serialize(D("jsonrpc","2.0","id",id,"error",D("code",code,"message",m))));Console.Out.Flush();}}
 static object[] Tools(){var properties=D("action",D("type","string"),"window",D("type","string","maxLength",80),"revision",D("type","string","maxLength",64),"ref",D("type","string","maxLength",32),"text",D("type","string","maxLength",4000),"key",D("type","string"),"x",D("type","integer"),"y",D("type","integer"),"direction",D("type","string"));var schema=D("type","object","properties",properties,"required",new[]{"action"},"additionalProperties",false);
 return new object[]{D("name","observe","description","Observe allowed Windows apps on the Owner desktop. Actions: status, windows, observe, screenshot. Supply returned window identity. UIA refs and screenshot coordinates bind to a fresh revision. Password values omitted. Model vision required for screenshot interpretation. Local only.","inputSchema",schema,"readOnly",true,"localOnly",true),D("name","act","description","Operate allowed Windows windows: activate, click, type, key, scroll, pause, resume, stop. User must enable control with the visible local panel; input/focus changes pause control. Use fresh observation revision and UIA ref or screenshot coordinates. Input dispatch is not evidence of a successful save/send. No terminal, command, password, UAC or lock-screen automation.","inputSchema",schema,"readOnly",false,"localOnly",true)};}
 static void Desktop(){if(!Environment.UserInteractive)throw new Fault("No interactive Windows desktop");IntPtr desktop=OpenInputDesktop(0,false,0x0100);if(desktop==IntPtr.Zero)throw new Fault("Desktop locked or secure desktop unavailable");CloseDesktop(desktop);var name=new StringBuilder(128);int size;desktop=OpenInputDesktop(0,false,0x0001);if(desktop==IntPtr.Zero)throw new Fault("Input desktop unavailable");try{if(!GetUserObjectInformation(desktop,2,name,256,out size)||!String.Equals(name.ToString(),"Default",StringComparison.OrdinalIgnoreCase))throw new Fault("Secure or non-default desktop is unavailable");}finally{CloseDesktop(desktop);}}
 static uint InputTick(){var x=new LASTINPUTINFO{Size=(uint)Marshal.SizeOf(typeof(LASTINPUTINFO))};if(!GetLastInputInfo(ref x))throw new Fault("Cannot observe user input");return x.Tick;}
 static Process ValidWindow(IntPtr hwnd){Desktop();if(!IsWindow(hwnd)||!IsWindowVisible(hwnd)||IsIconic(hwnd))throw new Fault("Window is closed, hidden or minimized");uint pid;GetWindowThreadProcessId(hwnd,out pid);var process=Process.GetProcessById((int)pid);if(Denied.Contains(process.ProcessName))throw new Fault("This application is outside desktop-control scope");string exe;try{exe=process.MainModule.FileName;}catch{throw new Fault("Window process cannot be inspected");}if(!Allowed.Any(x=>String.Equals(x,exe,StringComparison.OrdinalIgnoreCase)))throw new Fault("Window executable is not in allowed_executables");return process;}
 static string WindowID(IntPtr hwnd,Process p){return hwnd.ToInt64().ToString("x")+":"+p.Id+":"+p.StartTime.ToUniversalTime().Ticks;}
 static IntPtr Resolve(string id){var parts=id.Split(':');long handle,start;int pid;if(parts.Length!=3||!long.TryParse(parts[0],System.Globalization.NumberStyles.HexNumber,null,out handle)||!int.TryParse(parts[1],out pid)||!long.TryParse(parts[2],out start))throw new Fault("Use a window identity returned by windows");IntPtr hwnd=new IntPtr(handle);var p=ValidWindow(hwnd);if(p.Id!=pid||p.StartTime.ToUniversalTime().Ticks!=start)throw new Fault("Window identity expired; list windows again");return hwnd;}
 static object Windows(){Desktop();var rows=new List<object>();EnumWindows(delegate(IntPtr hwnd,IntPtr extra){try{var p=ValidWindow(hwnd);var text=new StringBuilder(512);GetWindowText(hwnd,text,text.Capacity);rows.Add(D("window",WindowID(hwnd,p),"title",text.ToString(),"process",p.ProcessName,"foreground",GetForegroundWindow()==hwnd));}catch(Exception err){try{uint pid;GetWindowThreadProcessId(hwnd,out pid);var p=Process.GetProcessById((int)pid);if(Allowed.Any(x=>String.Equals(Path.GetFileNameWithoutExtension(x),p.ProcessName,StringComparison.OrdinalIgnoreCase))){Console.Error.WriteLine("Allowed app window unavailable: "+err.GetType().Name+": "+err.Message);}}catch{}}return rows.Count<100;},IntPtr.Zero);return rows;}
 static object Call(string tool,Dictionary<string,object>a,string scope,bool vision){string action=S(a,"action");bool read=new[]{"status","windows","observe","screenshot"}.Contains(action);if(tool!="observe"&&tool!="act"||(tool=="observe")!=read)throw new Fault("Action belongs to the other tool");
  if(action=="status")return Result(D("state",Paused?"paused":"controlling","platform","windows","configured_executables",Allowed.Length,"local_only",true,"emergency_stop","Ctrl+Alt+Escape","instructions","Use resume to open the control panel, then locally select Enable control. User input pauses subsequent actions."));
  if(action=="windows")return Result(Windows());
  if(action=="pause"||action=="stop"){Paused=true;Views.Clear();Target=IntPtr.Zero;ShowHud();return Result(D("state",action=="stop"?"stopped":"paused","external_actions_not_undone",true));}
  if(action=="resume"){Desktop();Paused=true;ShowHud();return Result(D("state","waiting_for_local_user","next","Click Enable control on the local FreeAgent panel, then observe the target window."));}
  IntPtr hwnd=Resolve(S(a,"window"));
  if(action=="screenshot"&&!vision)throw new Fault("Selected model has no declared vision; use UIA observation");
  if(action=="observe"||action=="screenshot")return Observe(hwnd,scope,action=="screenshot");
  if(Paused)throw new Fault("Control paused; local user must enable it on the visible panel");
  if(InputTick()!=LastTick){Paused=true;ShowHud();throw new Fault("User input detected; paused for takeover. Enable locally and observe again.");}
  if(action=="activate"){ShowHud();if(!ActivateTarget(hwnd)){Paused=true;throw new Fault("Cannot activate target; ask local user to select it");}Target=hwnd;LastTick=InputTick();Views.Clear();return Result(D("activated",true,"next","observe"));}
  Snapshot view;string revision=S(a,"revision");if(!Views.TryGetValue(revision,out view)||view.Scope!=scope||view.Hwnd!=hwnd||DateTime.UtcNow-view.Time>TimeSpan.FromSeconds(45))throw new Fault("Observation expired; observe again");
  RECT current;GetWindowRect(hwnd,out current);if(!current.Equals(view.Rect)||GetDpiForWindow(hwnd)!=view.Dpi)throw new Fault("Window moved, resized or changed DPI; observe again");
  if(GetForegroundWindow()!=hwnd){Paused=true;ShowHud();throw new Fault("Focus changed; paused before input");}
  if(InputTick()!=LastTick){Paused=true;throw new Fault("User took control; observe after local resume");}
  int x=I(a,"x",-1),y=I(a,"y",-1);string reference=S(a,"ref");AutomationElement element=null;
  if(reference!=""){if(!view.Elements.TryGetValue(reference,out element))throw new Fault("Element reference expired");try{if(element.Current.IsOffscreen||!element.Current.IsEnabled||element.Current.ProcessId!=view.PID||element.Current.IsPassword)throw new Fault("Element unavailable or sensitive");var r=element.Current.BoundingRectangle;x=(int)(r.Left+r.Width/2)-view.Rect.Left;y=(int)(r.Top+r.Height/2)-view.Rect.Top;}catch(ElementNotAvailableException){throw new Fault("Element no longer exists; observe again");}}
  Views.Clear();bool sent=false;
  try{
   if(action=="type"){
    string text=S(a,"text");if(text.Length<1||text.Length>4000||text.Any(c=>Char.IsControl(c)&&c!='\n'&&c!='\t'))throw new Fault("Text must contain 1–4000 printable characters");
    var focused=AutomationElement.FocusedElement;if(focused==null||focused.Current.ProcessId!=view.PID||focused.Current.IsPassword||focused.Current.ControlType!=ControlType.Edit&&focused.Current.ControlType!=ControlType.Document)throw new Fault("Observe and focus a non-password editable element before typing");
    if(element!=null&&!Automation.Compare(element,focused))throw new Fault("The referenced element is not focused");
    sent=true;foreach(char c in text)Unicode(c);
   }else if(action=="key"){
    if(AutomationElement.FocusedElement!=null&&AutomationElement.FocusedElement.Current.IsPassword)throw new Fault("Sensitive input requires user takeover");string key=S(a,"key");var keys=new Dictionary<string,ushort>{{"Enter",13},{"Tab",9},{"Escape",27},{"Backspace",8},{"Delete",46},{"ArrowLeft",37},{"ArrowUp",38},{"ArrowRight",39},{"ArrowDown",40},{"Home",36},{"End",35},{"PageUp",33},{"PageDown",34}};ushort code;if(!keys.TryGetValue(key,out code))throw new Fault("Unsupported key; system/terminal shortcuts are unavailable");sent=true;Virtual(code);
   }else if(action=="click"||action=="scroll"){
    if(x<0||y<0||x>=view.Width||y>=view.Height)throw new Fault("Coordinates are outside the observed screenshot");
    var mapped=ProjectPoint(x,y,view.Width,view.Height,new Rectangle(view.Rect.Left,view.Rect.Top,view.Rect.Right-view.Rect.Left,view.Rect.Bottom-view.Rect.Top));int px=mapped.X,py=mapped.Y;
    var hit=WindowFromPoint(new POINT{X=px,Y=py});if(GetAncestor(hit,2)!=hwnd&&hit!=hwnd)throw new Fault("Point is covered by another window; observe again");
    if(!SetCursorPos(px,py))throw new Fault("Cannot move pointer");sent=true;
    if(action=="click"){Mouse(0x0002,0);Mouse(0x0004,0);}else{string direction=S(a,"direction");if(direction!="up"&&direction!="down")throw new Fault("direction must be up or down");Mouse(0x0800,direction=="up"?360:-360);}
   }else throw new Fault("Unsupported desktop action");
   LastTick=InputTick();Target=hwnd;return Result(D("input_dispatched",true,"next","Observe and verify application outcome. Dispatch does not prove save or external submission."));
  }catch(Fault e){if(sent)throw new Fault(e.Message,true);throw;}catch{throw new Fault("Desktop input outcome unknown; observe before repeating",sent);}
 }
 static object Observe(IntPtr hwnd,string scope,bool screenshot){var p=ValidWindow(hwnd);RECT rect;if(!GetWindowRect(hwnd,out rect)||rect.Right<=rect.Left||rect.Bottom<=rect.Top)throw new Fault("Window bounds unavailable");var view=new Snapshot{Hwnd=hwnd,PID=p.Id,Start=p.StartTime.ToUniversalTime().Ticks,Rect=rect,Dpi=GetDpiForWindow(hwnd),Time=DateTime.UtcNow,Scope=scope,Width=rect.Right-rect.Left,Height=rect.Bottom-rect.Top};string revision=Guid.NewGuid().ToString("N");Views.Clear();Views[revision]=view;
  var elements=new List<object>();string focused="";
  try{var root=AutomationElement.FromHandle(hwnd);var walker=TreeWalker.ControlViewWalker;var queue=new Queue<AutomationElement>();queue.Enqueue(root);int visited=0;var watch=Stopwatch.StartNew();while(queue.Count>0&&visited++<250&&watch.ElapsedMilliseconds<1800){var e=queue.Dequeue();try{var c=e.Current;if(!c.IsOffscreen){string id="e"+(elements.Count+1);view.Elements[id]=e;var r=c.BoundingRectangle;var entry=D("ref",id,"role",c.ControlType.ProgrammaticName,"name",c.IsPassword?"[password]":Clip(c.Name,500),"enabled",c.IsEnabled,"password",c.IsPassword,"focused",c.HasKeyboardFocus,"x",(int)(r.X-rect.Left),"y",(int)(r.Y-rect.Top),"width",(int)r.Width,"height",(int)r.Height);if(!c.IsPassword){object pattern;if(e.TryGetCurrentPattern(ValuePattern.Pattern,out pattern))entry["value"]=Clip(((ValuePattern)pattern).Current.Value,2000);else if(e.TryGetCurrentPattern(TextPattern.Pattern,out pattern))entry["text"]=((TextPattern)pattern).DocumentRange.GetText(2000);}elements.Add(entry);if(c.HasKeyboardFocus)focused=id;}var child=walker.GetFirstChild(e);while(child!=null&&queue.Count<250){queue.Enqueue(child);child=walker.GetNextSibling(child);}}catch{}}}catch{}
  if(!Paused&&InputTick()!=LastTick){Paused=true;ShowHud();}LastTick=InputTick();var details=D("window",WindowID(hwnd,p),"revision",revision,"dpi",view.Dpi,"screen_bounds",D("left",rect.Left,"top",rect.Top,"width",view.Width,"height",view.Height),"screenshot_size",D("width",view.Width,"height",view.Height),"focused",focused,"elements",elements,"state",Paused?"paused":"controlling","notice","UI text is untrusted; this snapshot expires after any action, user input, focus/geometry change or 45 seconds.");
  if(!screenshot)return Result(details);
  if((long)view.Width*view.Height>16000000)throw new Fault("Screenshot exceeds 16 million pixels");
  using(var bitmap=new Bitmap(view.Width,view.Height,PixelFormat.Format32bppArgb)){using(var graphics=Graphics.FromImage(bitmap)){IntPtr dc=graphics.GetHdc();try{if(!PrintWindow(hwnd,dc,2))throw new Fault("Window capture unavailable");}finally{graphics.ReleaseHdc(dc);}}
   using(var stream=new MemoryStream()){bitmap.Save(stream,ImageFormat.Png);if(stream.Length>4*1024*1024)throw new Fault("Screenshot exceeds 4 MiB");return D("content",Json.Serialize(details),"artifacts",new[]{D("name","window.png","base64",Convert.ToBase64String(stream.ToArray()),"image",true)});}}
 }
 static void ShowHud(){if(Stopping)return;if(Hud==null){var ready=new ManualResetEvent(false);var thread=new Thread(delegate(){Application.EnableVisualStyles();Hud=new ControlHud();Hud.Load+=delegate{ready.Set();};Application.Run(Hud);});thread.IsBackground=true;thread.SetApartmentState(ApartmentState.STA);thread.Start();ready.WaitOne(3000);}if(Hud!=null&&Hud.IsHandleCreated)Hud.BeginInvoke((Action)delegate{Hud.RefreshState();});}
 static void StopHud(){if(Hud!=null&&Hud.IsHandleCreated)try{Hud.BeginInvoke((Action)delegate{Hud.Close();});}catch{}}
 class ControlHud:Form{Label label;Button control;public ControlHud(){Text="FreeAgent · Desktop control";Font=new Font("Microsoft YaHei UI",9.5f);TopMost=true;ShowInTaskbar=true;ClientSize=new Size(480,120);StartPosition=FormStartPosition.Manual;Location=new Point(Screen.PrimaryScreen.WorkingArea.Right-510,Screen.PrimaryScreen.WorkingArea.Bottom-165);label=new Label{Left=16,Top=14,Width=448,Height=38};control=new Button{Left=16,Top=64,Width=208,Height=38,Text="Enable control · 本机启用"};control.Click+=delegate{Paused=false;LastTick=InputTick();Views.Clear();RefreshState();};var stop=new Button{Left=240,Top=64,Width=224,Height=38,Text="Pause / Stop · Ctrl+Alt+Esc"};stop.Click+=delegate{Paused=true;Target=IntPtr.Zero;Views.Clear();RefreshState();};Controls.AddRange(new Control[]{label,control,stop});FormClosing+=delegate{Paused=true;};Load+=delegate{RegisterHotKey(Handle,1,0x0001|0x0002,0x1b);RefreshState();};FormClosed+=delegate{UnregisterHotKey(Handle,1);Hud=null;};}
 public void RefreshState(){label.Text=Paused?"已暂停 · 本机用户决定何时恢复":"FreeAgent 正在控制 · 用户输入将暂停后续操作";}
 protected override void WndProc(ref Message m){if(m.Msg==0x0312){Paused=true;Target=IntPtr.Zero;Views.Clear();RefreshState();}base.WndProc(ref m);}}
 static string Clip(string value,int max){return value==null?"":value.Length<=max?value:value.Substring(0,max);}
 static void Send(INPUT input){var a=new[]{input};if(SendInput(1,a,Marshal.SizeOf(typeof(INPUT)))!=1)throw new Fault("Windows rejected input",true);}
 internal static Point ProjectPoint(int x,int y,int width,int height,Rectangle bounds){if(width<1||height<1||bounds.Width<1||bounds.Height<1||x<0||y<0||x>=width||y>=height)throw new ArgumentOutOfRangeException("point");return new Point(bounds.Left+(int)((double)x*bounds.Width/width),bounds.Top+(int)((double)y*bounds.Height/height));}
 static bool ActivateTarget(IntPtr hwnd){if(GetForegroundWindow()==hwnd)return true;uint unused;uint foregroundThread=GetWindowThreadProcessId(GetForegroundWindow(),out unused),thread=GetCurrentThreadId();bool attached=foregroundThread!=0&&foregroundThread!=thread&&AttachThreadInput(thread,foregroundThread,true);try{ShowWindow(hwnd,5);BringWindowToTop(hwnd);SetForegroundWindow(hwnd);try{AutomationElement.FromHandle(hwnd).SetFocus();}catch{}for(int i=0;i<10;i++){if(GetForegroundWindow()==hwnd)return true;Thread.Sleep(20);}return false;}finally{if(attached)AttachThreadInput(thread,foregroundThread,false);}}
 static void Unicode(char c){Send(new INPUT{Type=1,U=new UNION{K=new KEYBDINPUT{Scan=c,Flags=4}}});Send(new INPUT{Type=1,U=new UNION{K=new KEYBDINPUT{Scan=c,Flags=4|2}}});}
 static void Virtual(ushort code){Send(new INPUT{Type=1,U=new UNION{K=new KEYBDINPUT{VK=code}}});Send(new INPUT{Type=1,U=new UNION{K=new KEYBDINPUT{VK=code,Flags=2}}});}
 static void Mouse(uint flags,int data){Send(new INPUT{Type=0,U=new UNION{M=new MOUSEINPUT{Flags=flags,Data=data}}});}
 [StructLayout(LayoutKind.Sequential)]struct RECT{public int Left,Top,Right,Bottom;}
 [StructLayout(LayoutKind.Sequential)]struct POINT{public int X,Y;}
 [StructLayout(LayoutKind.Sequential)]struct LASTINPUTINFO{public uint Size,Tick;}
 [StructLayout(LayoutKind.Sequential)]struct INPUT{public uint Type;public UNION U;}
 [StructLayout(LayoutKind.Explicit)]struct UNION{[FieldOffset(0)]public MOUSEINPUT M;[FieldOffset(0)]public KEYBDINPUT K;}
 [StructLayout(LayoutKind.Sequential)]struct KEYBDINPUT{public ushort VK,Scan;public uint Flags,Time;public UIntPtr Extra;}
 [StructLayout(LayoutKind.Sequential)]struct MOUSEINPUT{public int X,Y,Data;public uint Flags,Time;public UIntPtr Extra;}
 delegate bool EnumCallback(IntPtr hwnd,IntPtr extra);
 [DllImport("user32.dll")]static extern bool EnumWindows(EnumCallback c,IntPtr e);
 [DllImport("user32.dll")]static extern bool IsWindow(IntPtr h);
 [DllImport("user32.dll")]static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")]static extern bool IsIconic(IntPtr h);
 [DllImport("user32.dll")]static extern uint GetWindowThreadProcessId(IntPtr h,out uint pid);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)]static extern int GetWindowText(IntPtr h,StringBuilder b,int max);
 [DllImport("user32.dll")]static extern bool GetWindowRect(IntPtr h,out RECT r);
 [DllImport("user32.dll")]static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")]static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")]static extern bool BringWindowToTop(IntPtr h);
 [DllImport("user32.dll")]static extern bool ShowWindow(IntPtr h,int cmd);
 [DllImport("user32.dll")]static extern bool AttachThreadInput(uint a,uint b,bool attach);
 [DllImport("kernel32.dll")]static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")]static extern uint GetDpiForWindow(IntPtr h);
 [DllImport("user32.dll")]static extern bool SetProcessDpiAwarenessContext(IntPtr v);
 [DllImport("user32.dll")]static extern bool GetLastInputInfo(ref LASTINPUTINFO i);
 [DllImport("user32.dll")]static extern uint SendInput(uint n,INPUT[] i,int size);
 [DllImport("user32.dll")]static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")]static extern IntPtr WindowFromPoint(POINT p);
 [DllImport("user32.dll")]static extern IntPtr GetAncestor(IntPtr hwnd,uint flags);
 [DllImport("user32.dll")]static extern bool PrintWindow(IntPtr hwnd,IntPtr dc,uint flags);
 [DllImport("user32.dll")]static extern IntPtr OpenInputDesktop(uint flags,bool inherit,uint access);
 [DllImport("user32.dll")]static extern bool CloseDesktop(IntPtr h);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)]static extern bool GetUserObjectInformation(IntPtr h,int index,StringBuilder text,int length,out int needed);
 [DllImport("user32.dll")]static extern bool RegisterHotKey(IntPtr h,int id,uint mods,uint key);
 [DllImport("user32.dll")]static extern bool UnregisterHotKey(IntPtr h,int id);
}
class TestFixture:Form{public TestFixture(string[]args){Text="FreeAgent C1 isolated test";Size=new Size(640,440);var edit=new TextBox{AccessibleName="Test text",Name="TestInput",Left=28,Top=34,Width=540,Multiline=true,Height=150};var label=new Label{AccessibleName="Result",Left=28,Top=250,Width=540,Height=70,Text="No test action yet"};var button=new Button{AccessibleName="Apply test",Left=28,Top=198,Width=160,Height=36,Text="Apply test"};button.Click+=delegate{label.Text="Saved: "+edit.Text;label.AccessibleName=label.Text;};Controls.AddRange(new Control[]{edit,button,label});if(args.Length>1&&args[1]=="negative")Location=new Point(-100,80);}}
}
