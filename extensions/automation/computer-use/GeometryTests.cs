using System;
using System.Drawing;
namespace FreeAgentAutomation {
class GeometryTests {
 public static int Main(){
  foreach(int dpi in new[]{96,120,144}){
   int size=640*dpi/96;var p=ComputerUse.ProjectPoint(320,200,640,400,new Rectangle(-800,50,size,400*dpi/96));
   if(p.X!=-800+size/2||p.Y!=50+200*dpi/96)throw new Exception("DPI/negative-origin mapping failed");
  }
  try{ComputerUse.ProjectPoint(640,0,640,400,new Rectangle(0,0,640,400));throw new Exception("Out-of-range point accepted");}catch(ArgumentOutOfRangeException){}
  Console.WriteLine("PASS: 100% / 125% / 150% coordinate mapping, negative origins and bounds");return 0;
 }
}
}
